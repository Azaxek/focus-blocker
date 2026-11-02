package engine

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Azaxek/focus-blocker/internal/rewards"
	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/store"
)

// force separates what the calendar says from what is really enforced: a
// free-time break pauses every window that allows it.
type force struct {
	all   []schedule.Occurrence // in force on the calendar
	eff   []schedule.Occurrence // actually blocking (all, minus anything paused)
	until time.Time             // when the blocking that is really on stops (zero if none)
	brk   *rewards.Break        // the running break, if any
}

func forceAt(cfg store.Config, now time.Time, loc *time.Location, ended map[string]bool, led rewards.Ledger) force {
	f := force{all: schedule.Active(cfg.Windows, now, loc, ended)}
	f.eff = f.all
	ws := cfg.Windows
	if led.OnBreak(now) {
		f.brk, f.eff, ws = led.Break, nil, nil
		for _, w := range cfg.Windows {
			if w.NoBreak {
				ws = append(ws, w)
			}
		}
		for _, o := range f.all {
			if o.Window.NoBreak {
				f.eff = append(f.eff, o)
			}
		}
	}
	f.until = schedule.BlockedUntil(ws, now, loc, ended)
	return f
}

// pauseEnd is when the focus time that a break could pause ends (zero if none is running).
func pauseEnd(cfg store.Config, now time.Time, loc *time.Location, ended map[string]bool) time.Time {
	var ws []schedule.Window
	for _, w := range cfg.Windows {
		if !w.NoBreak {
			ws = append(ws, w)
		}
	}
	return schedule.BlockedUntil(ws, now, loc, ended)
}

func rewardsView(cfg store.Config, now time.Time, loc *time.Location, ended map[string]bool, st store.State, f force) RewardsView {
	v := RewardsView{BalanceSec: st.Rewards.Balance(now), Items: []RewardItem{}, CapMin: cfg.RewardCap(), Streak: rewardStreak(st.Days, now, loc)}
	for _, r := range st.Rewards.Usable(now) {
		v.Items = append(v.Items, RewardItem{ID: r.ID, Reason: r.Reason, Left: r.Left, Expires: r.Expires})
	}
	if d := st.Days[dayKey(now, loc)]; d != nil {
		v.GrantedToday = d.RewardMin
	}
	if f.brk != nil {
		v.Break = &BreakView{Start: f.brk.Start, Until: f.brk.Until}
		return v
	}
	if end := pauseEnd(cfg, now, loc, ended); !end.IsZero() {
		v.PauseMaxSec = min(v.BalanceSec, int(end.Sub(now)/time.Second))
		v.CanPause = v.PauseMaxSec >= 60
	}
	return v
}

// rewardStreak counts consecutive days that earned a reward. A day with none
// yet does not break it.
func rewardStreak(days map[string]*store.DayStat, now time.Time, loc *time.Location) int {
	n := 0
	for i := 0; i < 400; i++ {
		d := days[dayKey(now.AddDate(0, 0, -i), loc)]
		switch {
		case d != nil && d.RewardMin > 0:
			n++
		case i == 0:
			// today has not had one yet
		default:
			return n
		}
	}
	return n
}

func (e *Engine) endedCopy() map[string]bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := make(map[string]bool, len(e.ended))
	for k := range e.ended {
		m[k] = true
	}
	return m
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func cleanReason(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 40 {
		s = string(r[:40])
	}
	if s == "" {
		return "Reward"
	}
	return s
}

// GrantReward adds free time (the caller has checked the passcode). It is
// valid until the end of the local day `days` days from today's start.
func (e *Engine) GrantReward(minutes int, reason string, days int) (rewards.Reward, error) {
	if minutes < rewards.MinGrant || minutes > rewards.MaxGrant {
		return rewards.Reward{}, fmt.Errorf("pick between %d and %d minutes", rewards.MinGrant, rewards.MaxGrant)
	}
	if days < 1 || days > rewards.MaxDays {
		return rewards.Reward{}, fmt.Errorf("it can stay valid for 1 to %d days", rewards.MaxDays)
	}
	now := e.Clock.Now()
	cfg := e.Store.Config()
	loc := cfg.Location()
	n := now.In(loc)
	dayStart := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	r := rewards.Reward{ID: newID(), Reason: cleanReason(reason), Seconds: minutes * 60, Granted: now, Expires: dayStart.AddDate(0, 0, days)}
	limit := cfg.RewardCap()
	var denied error
	err := e.Store.UpdateState(func(s *store.State) {
		d := s.Day(dayKey(now, loc))
		if d.RewardMin+minutes > limit {
			denied = fmt.Errorf("that would go over today's limit of %d minutes (%d already given)", limit, d.RewardMin)
			return
		}
		if denied = s.Rewards.Grant(r); denied != nil {
			return
		}
		d.RewardMin += minutes
		s.Rewards.Prune(now)
	})
	if err = errors.Join(denied, err); err != nil {
		return rewards.Reward{}, err
	}
	e.Store.Log(now, "reward", fmt.Sprintf("Granted %d min of free time for “%s” (good until %s)", minutes, r.Reason, r.Expires.In(loc).Format("Mon 3:04 PM")))
	e.event("reward", fmt.Sprintf("You earned %d minutes of free time", minutes), r.Reason)
	e.Wake()
	return r, nil
}

// RevokeReward takes back the unspent part of a reward.
func (e *Engine) RevokeReward(id string) error {
	now := e.Clock.Now()
	loc := e.Store.Config().Location()
	var gone int
	var reason string
	var rerr error
	err := e.Store.UpdateState(func(s *store.State) {
		var granted time.Time
		for _, r := range s.Rewards.Items {
			if r.ID == id {
				reason, granted = r.Reason, r.Granted
			}
		}
		if gone, rerr = s.Rewards.Revoke(id); rerr == nil { // it no longer counts against the day's limit
			if d := s.Days[dayKey(granted, loc)]; d != nil {
				d.RewardMin = max(0, d.RewardMin-gone/60)
			}
		}
	})
	if err = errors.Join(rerr, err); err != nil {
		return err
	}
	e.Store.Log(now, "reward", fmt.Sprintf("Took back %d min of unused free time (“%s”)", gone/60, reason))
	e.Wake()
	return nil
}

// StartBreak spends saved-up free time to pause the focus windows that allow
// it. minutes <= 0 means "as much as possible". It never pauses longer than
// the focus time that is actually running.
func (e *Engine) StartBreak(minutes int) (rewards.Break, error) {
	now := e.Clock.Now()
	cfg := e.Store.Config()
	loc := cfg.Location()
	ended := e.endedCopy()
	led := e.Store.Ledger()
	if led.OnBreak(now) {
		return rewards.Break{}, rewards.ErrOnBreak
	}
	f := forceAt(cfg, now, loc, ended, led)
	if len(f.all) == 0 {
		return rewards.Break{}, errors.New("there is no focus time to pause right now")
	}
	end := pauseEnd(cfg, now, loc, ended)
	if end.IsZero() {
		return rewards.Break{}, errors.New("this focus time cannot be paused")
	}
	sec := min(led.Balance(now), int(end.Sub(now)/time.Second))
	if minutes > 0 {
		sec = min(sec, minutes*60)
	}
	if led.Balance(now) < 60 {
		return rewards.Break{}, rewards.ErrNoCredit
	}
	if sec < 60 {
		return rewards.Break{}, errors.New("less than a minute of focus time is left")
	}
	var b rewards.Break
	var serr error
	if err := e.Store.UpdateState(func(s *store.State) { b, serr = s.Rewards.Start(now, sec) }); err != nil || serr != nil {
		return rewards.Break{}, errors.Join(serr, err)
	}
	e.Store.Log(now, "reward", fmt.Sprintf("Free time started: %d min, until %s", sec/60, b.Until.In(loc).Format("3:04 PM")))
	e.event("break-start", "Free time started", "Focus time is back at "+b.Until.In(loc).Format("3:04 PM"))
	e.Wake()
	return b, nil
}

// EndBreak stops a running break now; the unused minutes go back to the saved-up time.
func (e *Engine) EndBreak() (int, error) {
	now := e.Clock.Now()
	var back int
	var berr error
	if err := e.Store.UpdateState(func(s *store.State) { back, berr = s.Rewards.End(now) }); err != nil || berr != nil {
		return 0, errors.Join(berr, err)
	}
	e.Store.Log(now, "reward", fmt.Sprintf("Free time ended early; %d min saved for later", back/60))
	e.event("break-end", "Back to focus", fmt.Sprintf("%d minutes of free time are saved for later.", back/60))
	e.Wake()
	return back, nil
}

func (e *Engine) finishBreak(now time.Time, backToFocus bool) {
	var done bool
	e.Store.UpdateState(func(s *store.State) { done = s.Rewards.Finish(now); s.Rewards.Prune(now) })
	if done {
		e.Store.Log(now, "reward", "Free time is over")
		if backToFocus {
			e.event("break-end", "Free time is over", "Focus time is back on.")
		}
	}
}

// warnings sends each heads-up notice once: a window about to start, a break about to end.
func (e *Engine) warnings(now time.Time, loc *time.Location, f force, next schedule.Occurrence, hasNext bool) {
	if f.brk != nil && f.brk.Until.Sub(now) <= 2*time.Minute {
		e.once("break|"+f.brk.Start.UTC().Format(time.RFC3339), "break-soon", "Free time ends in 2 minutes", "Save what you are doing.")
	}
	if hasNext && len(f.eff) == 0 && f.brk == nil && next.Start.Sub(now) <= 5*time.Minute {
		e.once("soon|"+next.Key(), "window-soon", "Focus time starts in 5 minutes", fmt.Sprintf("%s at %s", next.Window.Name, next.Start.In(loc).Format("3:04 PM")))
	}
}

func (e *Engine) once(key, kind, title, body string) {
	e.mu.Lock()
	if e.warned[key] {
		e.mu.Unlock()
		return
	}
	if len(e.warned) > 200 {
		e.warned = map[string]bool{}
	}
	e.warned[key] = true
	e.mu.Unlock()
	e.event(kind, title, body)
}
