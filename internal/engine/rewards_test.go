package engine

import (
	"strings"
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/rewards"
	"github.com/Azaxek/focus-blocker/internal/schedule"
)

// monday 6pm inside a 3:30pm-9pm window
func monday(t *testing.T, ws ...schedule.Window) *harness {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 18, 0, 0, 0, loc))
	if len(ws) == 0 {
		ws = []schedule.Window{{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"}}
	}
	h.set(ws...)
	h.e.tick()
	return h
}

func (h *harness) kinds() string {
	var b strings.Builder
	for _, ev := range h.e.EventsSince(0) {
		b.WriteString(ev.Kind + ",")
	}
	return b.String()
}

func TestGrantRespectsTheDailyLimitAndRevokeFreesIt(t *testing.T) {
	h := monday(t)
	if _, err := h.e.GrantReward(30, "  Gym \n ", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := h.e.GrantReward(30, "Homework", 1); err != nil {
		t.Fatal(err) // 60 = exactly the default limit
	}
	if _, err := h.e.GrantReward(5, "Chores", 1); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over the limit: %v", err)
	}
	st := h.e.Status().Rewards
	if st.BalanceSec != 3600 || st.GrantedToday != 60 || st.CapMin != 60 || len(st.Items) != 2 || st.Items[0].Reason != "Gym" {
		t.Fatalf("%+v", st)
	}
	if err := h.e.RevokeReward(st.Items[1].ID); err != nil {
		t.Fatal(err)
	}
	if r := h.e.Status().Rewards; r.BalanceSec != 1800 || r.GrantedToday != 30 {
		t.Fatalf("revoked credit still counts: %+v", r)
	}
	if _, err := h.e.GrantReward(30, "Chores", 1); err != nil {
		t.Fatalf("limit should have room again: %v", err)
	}
	for _, c := range []struct{ m, d int }{{4, 1}, {121, 1}, {30, 0}, {30, 15}} {
		if _, err := h.e.GrantReward(c.m, "x", c.d); err == nil {
			t.Errorf("accepted minutes=%d days=%d", c.m, c.d)
		}
	}
	if !strings.Contains(h.logText(), "reward: Granted 30 min of free time for “Gym”") || !strings.Contains(h.kinds(), "reward,") {
		t.Fatalf("grant not logged/announced:\n%s", h.logText())
	}
}

func TestFreeTimePausesBlockingThenBlockingReturns(t *testing.T) {
	h := monday(t)
	h.e.GrantReward(30, "Gym", 1)
	if r := h.e.Status().Rewards; !r.CanPause || r.PauseMaxSec != 1800 {
		t.Fatalf("should be able to pause: %+v", r)
	}
	if _, err := h.e.StartBreak(0); err != nil {
		t.Fatal(err)
	}
	h.e.tick()
	st := h.e.Status()
	if st.Blocking || len(h.e.want.domains) != 0 || st.Rewards.Break == nil || len(st.Paused) != 1 || st.Rewards.CanPause {
		t.Fatalf("break should lift the block: %+v", st)
	}
	h.advance(29 * time.Minute)
	h.e.tick()
	if h.e.Status().Blocking || !strings.Contains(h.kinds(), "break-soon") {
		t.Fatalf("still on break, with a heads-up: %s", h.kinds())
	}
	h.advance(61 * time.Second)
	h.e.tick()
	st = h.e.Status()
	if !st.Blocking || len(h.e.want.domains) != 1 || st.Rewards.Break != nil || st.Rewards.BalanceSec != 0 {
		t.Fatalf("blocking must come back by itself: %+v", st)
	}
	if !strings.Contains(h.kinds(), "break-end") || !strings.Contains(h.logText(), "Free time is over") {
		t.Fatalf("end not announced: %s\n%s", h.kinds(), h.logText())
	}
	// and the window was never reported as ended or restarted by the break
	if strings.Contains(h.logText(), "Ended “School”") || strings.Count(h.logText(), "Started “School”") != 1 {
		t.Fatalf("a break is not a new window:\n%s", h.logText())
	}
}

func TestEndingABreakEarlyReturnsTheMinutes(t *testing.T) {
	h := monday(t)
	h.e.GrantReward(30, "Gym", 1)
	h.e.StartBreak(30)
	h.advance(10 * time.Minute)
	h.e.tick()
	back, err := h.e.EndBreak()
	if err != nil || back != 20*60 {
		t.Fatalf("refund %d, %v", back, err)
	}
	h.e.tick()
	st := h.e.Status()
	if !st.Blocking || st.Rewards.BalanceSec != 20*60 || st.Rewards.Break != nil {
		t.Fatalf("%+v", st)
	}
	if _, err := h.e.EndBreak(); err == nil {
		t.Fatal("ended a break that was not running")
	}
}

func TestBreakNeverOutlastsTheFocusTimeItPauses(t *testing.T) {
	h := monday(t, schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "18:10"})
	h.e.GrantReward(30, "Gym", 1)
	b, err := h.e.StartBreak(30)
	if err != nil {
		t.Fatal(err)
	}
	if got := b.Until.Sub(b.Start); got != 10*time.Minute {
		t.Fatalf("break is %v; only 10 minutes of focus time were left", got)
	}
	if h.e.Status().Rewards.BalanceSec != 20*60 {
		t.Fatal("unused credit must stay saved")
	}
}

func TestWindowsCanOptOutOfBreaks(t *testing.T) {
	exam := schedule.Window{ID: "exam", Name: "Exam prep", Days: []int{1}, Start: "17:00", End: "20:00", NoBreak: true}
	h := monday(t, exam)
	h.e.GrantReward(30, "Gym", 1)
	if r := h.e.Status().Rewards; r.CanPause {
		t.Fatal("a no-break window must not be pausable")
	}
	if _, err := h.e.StartBreak(30); err == nil {
		t.Fatal("paused an exam window")
	}

	// two windows at once: only the one that allows it is paused
	school := schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"}
	h2 := monday(t, school, exam)
	h2.e.GrantReward(30, "Gym", 1)
	if _, err := h2.e.StartBreak(30); err != nil {
		t.Fatal(err)
	}
	h2.e.tick()
	st := h2.e.Status()
	if !st.Blocking || len(st.Active) != 1 || st.Active[0].ID != "exam" || len(st.Paused) != 1 || st.Paused[0].ID != "w" {
		t.Fatalf("exam must keep blocking while School is paused: %+v", st)
	}
	if st.Until == nil || !st.Until.Equal(time.Date(2026, 5, 4, 20, 0, 0, 0, h2.loc)) {
		t.Fatalf("until must follow the window that is still on: %v", st.Until)
	}
}

func TestBreakSurvivesARestartAndNeedsAWindow(t *testing.T) {
	h := monday(t)
	h.e.GrantReward(30, "Gym", 1)
	h.e.StartBreak(30)
	e2 := New("test", h.st, h.e.Clock, h.site, h.app) // service restarted mid-break
	e2.tick()
	if e2.Status().Blocking {
		t.Fatal("a restart must not cut a break short")
	}

	loc := h.loc
	free := newHarness(t, time.Date(2026, 5, 4, 10, 0, 0, 0, loc))
	free.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"})
	free.e.GrantReward(30, "Gym", 1)
	if _, err := free.e.StartBreak(30); err == nil {
		t.Fatal("spent free time while nothing was blocked")
	}
	if free.e.Status().Rewards.BalanceSec != 1800 {
		t.Fatal("a refused break must not cost anything")
	}
}

func TestCreditExpiresAndTheLimitResetsNextDay(t *testing.T) {
	h := monday(t)
	h.e.GrantReward(30, "Gym", 1) // good until midnight
	h.e.GrantReward(30, "Homework", 2)
	h.advance(7 * time.Hour) // Tuesday 1am, outside any window
	h.e.tick()
	if r := h.e.Status().Rewards; r.BalanceSec != 1800 || len(r.Items) != 1 || r.Items[0].Reason != "Homework" {
		t.Fatalf("only the 2-day credit should be left: %+v", r)
	}
	if r := h.e.Status().Rewards; r.GrantedToday != 0 || r.Streak != 1 {
		t.Fatalf("a new day: %+v", r) // yesterday counted, today is still open
	}
	if _, err := h.e.GrantReward(60, "Gym", 1); err != nil {
		t.Fatalf("the daily limit must reset: %v", err)
	}
	if r := h.e.Status().Rewards; r.Streak != 2 {
		t.Fatalf("streak %d", r.Streak)
	}
}

func TestHeadsUpBeforeAWindowComesOnce(t *testing.T) {
	loc := chicago(t)
	h := newHarness(t, time.Date(2026, 5, 4, 15, 20, 0, 0, loc))
	h.set(schedule.Window{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"})
	h.e.tick()
	if strings.Contains(h.kinds(), "window-soon") {
		t.Fatal("10 minutes out is too early")
	}
	h.advance(6 * time.Minute)
	h.e.tick()
	h.advance(time.Second)
	h.e.tick()
	if strings.Count(h.kinds(), "window-soon") != 1 {
		t.Fatalf("want exactly one heads-up: %s", h.kinds())
	}
}

func TestAPanicInOnePassDoesNotStopTheEngine(t *testing.T) {
	h := monday(t)
	h.e.safely("test", func() { panic("boom") })
	ran := false
	h.e.safely("test", func() { ran = true })
	if !ran || !strings.Contains(h.logText(), "recovered): boom") {
		t.Fatalf("not recovered:\n%s", h.logText())
	}
}

func TestForceAtIsConsistent(t *testing.T) {
	loc := chicago(t)
	now := time.Date(2026, 5, 4, 18, 0, 0, 0, loc)
	cfg := monday(t).st.Config()
	var led rewards.Ledger
	led.Grant(rewards.Reward{ID: "a", Seconds: 1800, Granted: now, Expires: now.Add(time.Hour)})
	led.Start(now, 600)
	f := forceAt(cfg, now, loc, nil, led)
	if len(f.all) != 1 || len(f.eff) != 0 || f.brk == nil || !f.until.IsZero() {
		t.Fatalf("%+v", f)
	}
	f = forceAt(cfg, now.Add(11*time.Minute), loc, nil, led)
	if len(f.eff) != 1 || f.brk != nil || f.until.IsZero() {
		t.Fatalf("break over: %+v", f)
	}
}
