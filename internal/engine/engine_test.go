package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/clock"
	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/store"
)

type fakeSites struct {
	mu      sync.Mutex
	domains []string
	until   time.Time
	calls   int
}

func (f *fakeSites) Reconcile(d []string, u time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.domains, f.until, f.calls = d, u, f.calls+1
	return nil, nil
}
func (f *fakeSites) get() []string { f.mu.Lock(); defer f.mu.Unlock(); return f.domains }

type fakeApps struct {
	mu    sync.Mutex
	rules []apps.Rule
	kill  []apps.Killed
}

func (f *fakeApps) SetRules(r []apps.Rule) { f.mu.Lock(); f.rules = r; f.mu.Unlock() }
func (f *fakeApps) Scan() []apps.Killed {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.rules) == 0 {
		return nil
	}
	k := f.kill
	f.kill = nil
	return k
}

// harness builds an engine whose trusted clock the test controls.
type harness struct {
	e    *Engine
	st   *store.Store
	site *fakeSites
	app  *fakeApps
	wall *time.Time
	loc  *time.Location

	advance func(time.Duration)
}

func newHarness(t *testing.T, start time.Time) *harness {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := start
	var mono time.Duration
	clk := clock.NewWith(func() time.Time { return w }, func() time.Duration { return mono })
	h := &harness{st: st, site: &fakeSites{}, app: &fakeApps{}, wall: &w, loc: start.Location()}
	h.e = New("test", st, clk, h.site, h.app)
	// advance moves wall and monotonic together, like real time passing
	h.advance = func(d time.Duration) { w = w.Add(d); mono += d }
	return h
}

func (h *harness) set(ws ...schedule.Window) {
	c := h.st.Config()
	c.Windows = ws
	c.Sites = []string{"youtube.com"}
	c.Apps = []string{"steam"}
	c.Timezone = h.loc.String()
	if err := h.st.SetConfig(c); err != nil {
		panic(err)
	}
}

func (h *harness) logText() string {
	var b strings.Builder
	for _, en := range h.st.ReadLog(100) {
		b.WriteString(en.Kind + ": " + en.Msg + "\n")
	}
	return b.String()
}

func chicago(t *testing.T) *time.Location {
	l, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip(err)
	}
	return l
}

func TestWindowLifecycleBlocksThenReleases(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 15, 29, 55, 0, loc)) // Monday
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "15:31"})

	h.e.tick()
	if len(h.e.Status().Active) != 0 {
		t.Fatal("not yet active")
	}
	h.advance(6 * time.Second)
	h.app.kill = []apps.Killed{{PID: 1, Name: "steam.exe", Rule: "Steam"}}
	h.e.tick()
	st := h.e.Status()
	if !st.Blocking || st.Sites != 1 || st.Apps != 1 {
		t.Fatalf("expected blocking 1 site 1 app, got %+v", st)
	}
	if got := h.e.want.domains; len(got) != 1 || got[0] != "youtube.com" {
		t.Fatalf("watchdog target %v", got)
	}
	ev := h.e.EventsSince(0)
	kinds := ""
	for _, e := range ev {
		kinds += e.Kind + ","
	}
	if !strings.Contains(kinds, "window-start") || !strings.Contains(kinds, "app-closed") {
		t.Fatalf("events %s", kinds)
	}
	if !strings.Contains(h.logText(), "Closed steam.exe") {
		t.Fatalf("log:\n%s", h.logText())
	}

	h.advance(90 * time.Second) // window is over
	h.e.tick()
	if h.e.Status().Blocking || len(h.e.want.domains) != 0 {
		t.Fatal("everything must be released after the window")
	}
	if !strings.Contains(h.logText(), "Ended “School”") {
		t.Fatalf("log:\n%s", h.logText())
	}
}

func TestBootMidWindowEnforcesImmediately(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 18, 0, 0, 0, loc))
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"})
	h.e.tick() // first tick of a fresh process
	if !h.e.Status().Blocking || len(h.e.want.domains) != 1 {
		t.Fatal("a service starting inside a window must enforce on its first tick")
	}
}

func TestEndEarlyIsPerOccurrenceAndLogged(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 16, 0, 0, 0, loc))
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1, 2}, Start: "15:30", End: "21:00"})
	h.e.tick()
	key := h.e.Status().Active[0].Key
	if err := h.e.EndEarly("bogus"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := h.e.EndEarly(key); err != nil {
		t.Fatal(err)
	}
	h.e.tick()
	if h.e.Status().Blocking {
		t.Fatal("window should be over")
	}
	h.advance(24 * time.Hour) // Tuesday, same window again
	h.e.tick()
	if !h.e.Status().Blocking {
		t.Fatal("next occurrence must still be enforced")
	}
	// the early end survives a restart
	e2 := New("test", h.st, h.e.Clock, h.site, h.app)
	if !e2.ended[key] {
		t.Fatal("ended-early lost across restart")
	}
	if !strings.Contains(h.logText(), "early") {
		t.Fatalf("log:\n%s", h.logText())
	}
}

func TestClockTamperDoesNotEndWindow(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 16, 0, 0, 0, loc))
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"})
	h.e.Clock.OnTamper = h.e.Tamper
	h.e.tick()
	*h.wall = h.wall.Add(6 * time.Hour) // set the PC clock to 10pm
	h.e.tick()
	if !h.e.Status().Blocking {
		t.Fatal("moving the clock forward must not end the window")
	}
	if !strings.Contains(h.logText(), "tamper: system clock jumped") {
		t.Fatalf("tamper not logged:\n%s", h.logText())
	}
}

func TestStreakAndFocusAccounting(t *testing.T) {
	loc := chicago(t)
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, loc)
	days := map[string]*store.DayStat{
		"2026-05-03": {FocusSec: 3600},
		"2026-05-02": {FocusSec: 600},
		"2026-05-01": {FocusSec: 600, EndedEarly: 1}, // breaks the streak
		"2026-04-30": {FocusSec: 600},
	}
	if got := streak(days, now, loc); got != 2 {
		t.Fatalf("streak = %d, want 2 (today empty does not break it)", got)
	}
	days["2026-05-04"] = &store.DayStat{FocusSec: 120}
	if got := streak(days, now, loc); got != 3 {
		t.Fatalf("streak = %d, want 3", got)
	}
}

func TestRunStopsOnCancel(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 10, 0, 0, 0, loc))
	h.set()
	h.e.Clock.Fetch = func(context.Context, func() time.Time) (time.Time, error) { return *h.wall, nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.e.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
}
