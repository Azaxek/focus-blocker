// Package engine is the service's brain: once a second it asks the trusted
// clock which windows are in force, tells the site and app blockers what to
// block, and records what happened.
package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/clock"
	"github.com/Azaxek/focus-blocker/internal/quotes"
	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/store"
)

// SiteBlocker is implemented by sites.Blocker.
type SiteBlocker interface {
	Reconcile(domains []string, until time.Time) (repaired []string, err error)
}

// AppBlocker is implemented by apps.Enforcer.
type AppBlocker interface {
	SetRules([]apps.Rule)
	Scan() []apps.Killed
}

type Event struct {
	ID    int       `json:"id"`
	T     time.Time `json:"t"`
	Kind  string    `json:"kind"` // app-closed, window-start, window-end, tamper
	Title string    `json:"title"`
	Body  string    `json:"body"`
}

type Engine struct {
	Version string
	Store   *store.Store
	Clock   *clock.Clock
	Sites   SiteBlocker
	Apps    AppBlocker

	mu       sync.Mutex
	ended    map[string]bool
	active   map[string]schedule.Occurrence
	blocking bool                 // something was actually being enforced at the last tick
	warned   map[string]bool      // heads-up notices already sent
	noted    map[string]time.Time // when a pop-up for a blocked app or site was last queued
	want     siteWant
	events   []Event
	nextID   int
	focus    time.Duration // focus time not yet saved
	lastTick time.Time
	lastSave time.Time
	sessSite int
	sessApp  int
	problem  string
	lastErr  map[string]time.Time
	wake     chan struct{}
	siteWake chan struct{}
	syncNow  chan struct{}
}

type siteWant struct {
	domains []string
	until   time.Time
}

func New(version string, st *store.Store, clk *clock.Clock, sites SiteBlocker, ap AppBlocker) *Engine {
	e := &Engine{Version: version, Store: st, Clock: clk, Sites: sites, Apps: ap,
		ended: map[string]bool{}, active: map[string]schedule.Occurrence{}, lastErr: map[string]time.Time{}, warned: map[string]bool{}, noted: map[string]time.Time{},
		wake: make(chan struct{}, 1), siteWake: make(chan struct{}, 1), syncNow: make(chan struct{}, 1), nextID: 1}
	for k := range st.State().Ended {
		e.ended[k] = true
	}
	return e
}

// Wake asks the loops to re-evaluate immediately (after a config change).
func (e *Engine) Wake() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	e.lastTick = time.Now()
	e.safely("tick", e.tick)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); e.siteLoop(ctx) }()
	go func() { defer wg.Done(); e.syncLoop(ctx) }()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			e.flushFocus()
			wg.Wait()
			return
		case <-t.C:
		case <-e.wake:
		}
		e.safely("tick", e.tick)
	}
}

// safely runs one pass of a loop and turns a panic into a log line, so a bug in
// one pass cannot stop enforcement; the next pass simply runs again.
func (e *Engine) safely(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			e.Store.Log(time.Now(), "error", fmt.Sprintf("internal error in %s (recovered): %v", what, r))
		}
	}()
	fn()
}

func (e *Engine) tick() {
	now := e.Clock.Now()
	cfg := e.Store.Config()
	loc := cfg.Location()

	led := e.Store.Ledger()

	e.mu.Lock()
	f := forceAt(cfg, now, loc, e.ended, led)
	sites, appIDs := schedule.Resolve(f.eff, cfg.Sites, cfg.Apps)
	e.want = siteWant{sites, f.until}
	next, hasNext := schedule.Next(cfg.Windows, now, loc, e.ended)

	cur := map[string]schedule.Occurrence{}
	for _, o := range f.all {
		cur[o.Key()] = o
	}
	var started, finished []schedule.Occurrence
	for k, o := range cur {
		if _, ok := e.active[k]; !ok {
			started = append(started, o)
		}
	}
	for k, o := range e.active {
		if _, ok := cur[k]; !ok {
			finished = append(finished, o)
		}
	}
	wasActive := len(e.active) > 0
	e.active = cur
	wasBlocking := e.blocking
	e.blocking = len(f.eff) > 0

	dt := time.Since(e.lastTick)
	e.lastTick = time.Now()
	if e.blocking && dt > 0 && dt < 5*time.Second { // a free-time break is not focus time
		e.focus += dt
	}
	var summary string
	if wasActive && len(cur) == 0 {
		summary = fmt.Sprintf("%d site attempts blocked, %d apps closed", e.sessSite, e.sessApp)
		e.sessSite, e.sessApp = 0, 0
	}
	e.mu.Unlock()

	for _, o := range started {
		e.Store.Log(now, "window", fmt.Sprintf("Started “%s” until %s", o.Window.Name, o.End.In(loc).Format("Mon 3:04 PM")))
		e.event("window-start", "Focus time started", fmt.Sprintf("%s until %s. %s", o.Window.Name, o.End.In(loc).Format("3:04 PM"), quotes.Random(quotes.Start)))
	}
	for _, o := range finished {
		e.mu.Lock()
		early := e.ended[o.Key()]
		e.mu.Unlock()
		if !early {
			e.Store.Log(now, "window", fmt.Sprintf("Ended “%s”", o.Window.Name))
			e.event("window-end", "Free time", fmt.Sprintf("“%s” is over. %s", o.Window.Name, quotes.Random(quotes.Done)))
		}
	}
	if summary != "" {
		e.Store.Log(now, "window", "All windows over: "+summary)
	}
	if led.Break != nil && f.brk == nil { // the break's time is up; blocking resumed in this same pass
		e.finishBreak(now, len(f.all) > 0)
	}
	e.warnings(now, loc, f, next, hasNext)

	e.Apps.SetRules(cfg.Rules(appIDs))
	for _, k := range e.Apps.Scan() {
		e.mu.Lock()
		e.sessApp++
		e.mu.Unlock()
		e.Store.UpdateState(func(s *store.State) { s.Day(dayKey(now, loc)).AppBlocks++ })
		e.Store.Log(now, "blocked-app", fmt.Sprintf("Closed %s (%s)", k.Name, k.Rule))
		if e.due("app:"+k.Rule, 30*time.Second) { // a program that keeps relaunching gets one pop-up, not one a second
			e.event("app-closed", k.Rule+" is blocked", quotes.Random(quotes.Block))
		}
	}

	if e.Clock != nil && time.Since(e.lastSave) > time.Minute {
		e.flushFocus()
	}
	if wasBlocking != (len(f.eff) > 0) || len(started) > 0 || len(finished) > 0 {
		e.flushFocus()
		e.pruneEnded(now)
		e.wakeSites()
	}
}

func dayKey(t time.Time, loc *time.Location) string { return t.In(loc).Format("2006-01-02") }

func (e *Engine) flushFocus() {
	e.mu.Lock()
	secs := int(e.focus.Seconds())
	e.focus -= time.Duration(secs) * time.Second
	e.lastSave = time.Now()
	e.mu.Unlock()
	if secs == 0 {
		return
	}
	now := e.Clock.Now()
	loc := e.Store.Config().Location()
	e.Store.UpdateState(func(s *store.State) { s.Day(dayKey(now, loc)).FocusSec += secs })
}

func (e *Engine) pruneEnded(now time.Time) {
	var drop []string
	for k, end := range e.Store.State().Ended {
		if end.Before(now.Add(-48 * time.Hour)) {
			drop = append(drop, k)
		}
	}
	if len(drop) == 0 {
		return
	}
	e.Store.UpdateState(func(s *store.State) {
		for _, k := range drop {
			delete(s.Ended, k)
		}
	})
	e.mu.Lock()
	for _, k := range drop {
		delete(e.ended, k)
	}
	e.mu.Unlock()
}

func (e *Engine) event(kind, title, body string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, Event{ID: e.nextID, T: time.Now(), Kind: kind, Title: title, Body: body})
	e.nextID++
	if len(e.events) > 50 {
		e.events = e.events[len(e.events)-50:]
	}
}

// EventsSince returns events with an id greater than after.
func (e *Engine) EventsSince(after int) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, ev := range e.events {
		if ev.ID > after {
			out = append(out, ev)
		}
	}
	return out
}

// Tamper records something that looks like an attempt to get around the lock.
func (e *Engine) Tamper(msg string) {
	e.Store.Log(e.Clock.Now(), "tamper", msg)
	e.event("tamper", "Tamper attempt noticed", msg)
}

// SiteAttempt counts a blocked website visit.
func (e *Engine) SiteAttempt(domain string) {
	now := e.Clock.Now()
	loc := e.Store.Config().Location()
	e.mu.Lock()
	e.sessSite++
	e.mu.Unlock()
	e.Store.UpdateState(func(s *store.State) { s.Day(dayKey(now, loc)).SiteBlocks++ })
}

// BlockedNow is the list of domains being blocked at this moment. The tray uses it
// to tell whether a browser tab is showing a blocked site.
func (e *Engine) BlockedNow() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string{}, e.want.domains...)
}

// due reports whether key has not been used in the last gap, and marks it used.
func (e *Engine) due(key string, gap time.Duration) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if time.Since(e.noted[key]) < gap {
		return false
	}
	e.noted[key] = time.Now()
	return true
}

// EndEarly ends one active occurrence (the caller has checked the passcode).
func (e *Engine) EndEarly(key string) error {
	e.mu.Lock()
	o, ok := e.active[key]
	e.mu.Unlock()
	if !ok {
		return errors.New("that window is not active")
	}
	now := e.Clock.Now()
	loc := e.Store.Config().Location()
	if err := e.Store.UpdateState(func(s *store.State) {
		s.Ended[key] = o.End
		s.Day(dayKey(now, loc)).EndedEarly++
	}); err != nil {
		return err
	}
	e.mu.Lock()
	e.ended[key] = true
	e.mu.Unlock()
	e.Store.Log(now, "window", fmt.Sprintf("Ended “%s” early (passcode)", o.Window.Name))
	e.event("window-end", "Ended early", o.Window.Name)
	e.Wake()
	return nil
}

// Resume turns a window that was ended early back on for the rest of its time.
// It only makes things stricter, so unlike EndEarly it needs no passcode. The
// early end stays on the day's record: the streak is not given back.
func (e *Engine) Resume(key string) error {
	now := e.Clock.Now()
	cfg := e.Store.Config()
	var o *schedule.Occurrence
	for _, c := range schedule.Occurrences(cfg.Windows, now, now.Add(time.Nanosecond), cfg.Location()) {
		if c.Key() == key && !c.Start.After(now) {
			o = &c
		}
	}
	e.mu.Lock()
	ended := e.ended[key]
	e.mu.Unlock()
	if o == nil || !ended {
		return errors.New("that window was not ended early, or it is already over")
	}
	if err := e.Store.UpdateState(func(s *store.State) { delete(s.Ended, key) }); err != nil {
		return err
	}
	e.mu.Lock()
	delete(e.ended, key)
	e.mu.Unlock()
	e.Store.Log(now, "window", fmt.Sprintf("Turned “%s” back on", o.Window.Name))
	e.Wake()
	return nil
}

func (e *Engine) wakeSites() {
	select {
	case e.siteWake <- struct{}{}:
	default:
	}
}

// Shutdown clears every block. Only used for a passcode-approved stop or uninstall;
// a crash deliberately leaves the rules in place.
func (e *Engine) Shutdown() error {
	e.mu.Lock()
	e.want = siteWant{}
	e.mu.Unlock()
	e.Apps.SetRules(nil)
	_, err := e.Sites.Reconcile(nil, time.Time{})
	return err
}
