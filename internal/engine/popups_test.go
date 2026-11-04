package engine

import (
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/schedule"
)

func TestResumeTurnsAnEarlyEndedWindowBackOn(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 16, 0, 0, 0, loc)) // Monday
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"})
	h.e.tick()
	key := h.e.Status().Active[0].Key

	if err := h.e.Resume(key); err == nil {
		t.Fatal("a window that was never ended must not be 'resumed'")
	}
	if err := h.e.EndEarly(key); err != nil {
		t.Fatal(err)
	}
	h.e.tick()
	if h.e.Status().Blocking {
		t.Fatal("ended early should stop blocking")
	}

	if err := h.e.Resume("bogus"); err == nil {
		t.Fatal("unknown key accepted")
	}
	if err := h.e.Resume(key); err != nil {
		t.Fatal(err)
	}
	h.e.tick()
	st := h.e.Status()
	if !st.Blocking || len(st.Active) != 1 || len(h.e.want.domains) != 1 {
		t.Fatalf("blocking should be back: %+v", st)
	}
	if got := h.st.State().Days["2026-05-04"].EndedEarly; got != 1 {
		t.Fatalf("the early end must stay on the record, got %d", got)
	}
	if h.st.State().Ended[key] != (time.Time{}) {
		t.Fatal("the early end was not cleared from the saved state")
	}
	e2 := New("test", h.st, h.e.Clock, h.site, h.app) // and it survives a restart
	if e2.ended[key] {
		t.Fatal("resumed window is ended again after a restart")
	}
	if n, ev := h.eventsOf("window-start"); n < 2 || len(ev.Body) < 10 {
		t.Fatalf("expected a fresh start notice with a line, got %d (%q)", n, ev.Body)
	}

	h.e.EndEarly(key) // ended again, and this time the window runs out
	h.advance(6 * time.Hour)
	h.e.tick()
	if err := h.e.Resume(key); err == nil {
		t.Fatal("a window that is already over cannot be resumed")
	}
}

func (h *harness) eventsOf(kind string) (n int, last Event) {
	for _, ev := range h.e.EventsSince(0) {
		if ev.Kind == kind {
			n, last = n+1, ev
		}
	}
	return n, last
}

// A relaunching program must not pop up once a second, and a blocked-site lookup
// must never pop up at all: lookups come from background tabs and programs all day
// (a single YouTube tab did it every minute), so they are counted, not announced.
// The tray decides about sites from what the browser in front is showing.
func TestBlockedPopupsAreRareAndCarryAQuote(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 15, 30, 0, 0, loc)) // Monday
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "16:30"})
	if got := h.e.BlockedNow(); len(got) != 0 {
		t.Fatalf("nothing is blocked before the window: %v", got)
	}
	h.e.tick()
	if got := h.e.BlockedNow(); len(got) != 1 || got[0] != "youtube.com" {
		t.Fatalf("blocked now: %v", got)
	}

	for range 5 {
		h.e.SiteAttempt("youtube.com")
	}
	if n, _ := h.eventsOf("site-blocked"); n != 0 {
		t.Fatalf("a lookup produced %d pop-up events; lookups are not visits", n)
	}
	if got := h.st.State().Days["2026-05-04"].SiteBlocks; got != 5 {
		t.Fatalf("every attempt must still be counted, got %d", got)
	}

	for range 3 { // the program keeps coming back
		h.app.kill = []apps.Killed{{PID: 1, Name: "steam.exe", Rule: "Steam"}}
		h.e.tick()
	}
	if n, ev := h.eventsOf("app-closed"); n != 1 || ev.Title != "Steam is blocked" || len(ev.Body) < 10 {
		t.Fatalf("%d app pop-ups (%q), want 1", n, ev.Title)
	}
	if got := h.st.State().Days["2026-05-04"].AppBlocks; got != 3 {
		t.Fatalf("every close must still be counted, got %d", got)
	}
	if h.e.Status().Quote == "" {
		t.Fatal("the dashboard needs a line of the day")
	}
}
