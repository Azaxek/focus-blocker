package engine

import (
	"time"

	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/store"
)

type OccView struct {
	Key   string    `json:"key"`
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Ended bool      `json:"ended,omitempty"` // ended early by the parent
}

type Status struct {
	Version   string     `json:"version"`
	Now       time.Time  `json:"now"`
	Zone      string     `json:"zone"`
	Blocking  bool       `json:"blocking"`
	Active    []OccView  `json:"active"`
	Until     *time.Time `json:"until,omitempty"` // when continuous blocking ends
	Next      *OccView   `json:"next,omitempty"`
	Today     []OccView  `json:"today"`
	DayStart  time.Time  `json:"dayStart"`
	DayEnd    time.Time  `json:"dayEnd"`
	Sites     int        `json:"sites"` // domains blocked right now
	Apps      int        `json:"apps"`
	Streak    int        `json:"streak"`
	FocusDay  float64    `json:"focusHoursToday"`
	FocusWeek float64    `json:"focusHoursWeek"`
	SiteHits  int        `json:"siteBlocksToday"`
	AppHits   int        `json:"appBlocksToday"`
	TimeSync  bool       `json:"timeSynced"`
	Problem   string     `json:"problem,omitempty"`
}

func view(o schedule.Occurrence, ended map[string]bool) OccView {
	return OccView{Key: o.Key(), ID: o.Window.ID, Name: o.Window.Name, Start: o.Start, End: o.End, Ended: ended[o.Key()]}
}

// Status is everything the dashboard shows. It needs no passcode.
func (e *Engine) Status() Status {
	now := e.Clock.Now()
	cfg := e.Store.Config()
	loc := cfg.Location()
	e.mu.Lock()
	ended := make(map[string]bool, len(e.ended))
	for k := range e.ended {
		ended[k] = true
	}
	problem := e.problem
	e.mu.Unlock()

	n := now.In(loc)
	dayStart := time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)
	s := Status{Version: e.Version, Now: now, Zone: loc.String(), DayStart: dayStart, DayEnd: dayEnd, Problem: problem,
		Active: []OccView{}, Today: []OccView{}}
	s.TimeSync, _ = e.Clock.Synced()

	active := schedule.Active(cfg.Windows, now, loc, ended)
	for _, o := range active {
		s.Active = append(s.Active, view(o, ended))
	}
	s.Blocking = len(active) > 0
	if u := schedule.BlockedUntil(cfg.Windows, now, loc, ended); !u.IsZero() {
		s.Until = &u
	}
	if nx, ok := schedule.Next(cfg.Windows, now, loc, ended); ok {
		v := view(nx, ended)
		s.Next = &v
	}
	for _, o := range schedule.Occurrences(cfg.Windows, dayStart, dayEnd, loc) {
		s.Today = append(s.Today, view(o, ended))
	}
	sites, appIDs := schedule.Resolve(active, cfg.Sites, cfg.Apps)
	s.Sites, s.Apps = len(sites), len(cfg.Rules(appIDs))

	days := e.Store.State().Days
	today := days[dayKey(now, loc)]
	if today != nil {
		s.FocusDay = hours(today.FocusSec) + e.pendingHours()
		s.SiteHits, s.AppHits = today.SiteBlocks, today.AppBlocks
	} else {
		s.FocusDay = e.pendingHours()
	}
	for i := 0; i < 7; i++ {
		if d := days[dayKey(now.AddDate(0, 0, -i), loc)]; d != nil {
			s.FocusWeek += hours(d.FocusSec)
		}
	}
	s.FocusWeek += e.pendingHours()
	s.Streak = streak(days, now, loc)
	return s
}

func hours(sec int) float64 { return float64(sec) / 3600 }

func (e *Engine) pendingHours() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.focus.Hours()
}

// streak counts consecutive days with focus time and no early ending. A day
// that has not had any focus yet does not break the streak.
func streak(days map[string]*store.DayStat, now time.Time, loc *time.Location) int {
	n := 0
	for i := 0; i < 400; i++ {
		d := days[dayKey(now.AddDate(0, 0, -i), loc)]
		good := d != nil && d.FocusSec >= 60 && d.EndedEarly == 0
		switch {
		case good:
			n++
		case i == 0 && (d == nil || d.FocusSec < 60):
			// today has not started yet
		default:
			return n
		}
	}
	return n
}
