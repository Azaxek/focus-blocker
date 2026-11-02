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
		ended: map[string]bool{}, active: map[string]schedule.Occurrence{}, lastErr: map[string]time.Time{},
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
	e.tick()
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
		e.tick()
	}
}

func (e *Engine) tick() {
	now := e.Clock.Now()
	cfg := e.Store.Config()
	loc := cfg.Location()

	e.mu.Lock()
	active := schedule.Active(cfg.Windows, now, loc, e.ended)
	until := schedule.BlockedUntil(cfg.Windows, now, loc, e.ended)
	sites, appIDs := schedule.Resolve(active, cfg.Sites, cfg.Apps)
	e.want = siteWant{sites, until}

	cur := map[string]schedule.Occurrence{}
	for _, o := range active {
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

	dt := time.Since(e.lastTick)
	e.lastTick = time.Now()
	if len(cur) > 0 && dt > 0 && dt < 5*time.Second {
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
		e.event("window-start", "Focus time started", fmt.Sprintf("%s — until %s", o.Window.Name, o.End.In(loc).Format("3:04 PM")))
	}
	for _, o := range finished {
		e.mu.Lock()
		early := e.ended[o.Key()]
		e.mu.Unlock()
		if !early {
			e.Store.Log(now, "window", fmt.Sprintf("Ended “%s”", o.Window.Name))
			e.event("window-end", "Free time", fmt.Sprintf("“%s” is over", o.Window.Name))
		}
	}
	if summary != "" {
		e.Store.Log(now, "window", "All windows over: "+summary)
	}

	e.Apps.SetRules(cfg.Rules(appIDs))
	for _, k := range e.Apps.Scan() {
		e.mu.Lock()
		e.sessApp++
		e.mu.Unlock()
		e.Store.UpdateState(func(s *store.State) { s.Day(dayKey(now, loc)).AppBlocks++ })
		e.Store.Log(now, "blocked-app", fmt.Sprintf("Closed %s (%s)", k.Name, k.Rule))
		e.event("app-closed", k.Rule+" is blocked", "Closed during your focus time.")
	}

	if e.Clock != nil && time.Since(e.lastSave) > time.Minute {
		e.flushFocus()
	}
	if wasActive != (len(cur) > 0) || len(started) > 0 || len(finished) > 0 {
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
