// Package schedule turns recurring and one-off windows into concrete
// occurrences. All wall-clock arithmetic happens in the supplied location, so
// midnight crossings and DST shifts follow the local calendar.
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

type Window struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Days     []int    `json:"days,omitempty"` // recurring: 0=Sunday .. 6=Saturday
	Date     string   `json:"date,omitempty"` // one-off: YYYY-MM-DD
	Start    string   `json:"start"`          // HH:MM
	End      string   `json:"end"`            // HH:MM; at or before Start means "next day"
	OwnLists bool     `json:"ownLists,omitempty"`
	Sites    []string `json:"sites,omitempty"` // own list: domains
	Apps     []string `json:"apps,omitempty"`  // own list: app rule IDs
	Disabled bool     `json:"disabled,omitempty"`
	NoBreak  bool     `json:"noBreak,omitempty"` // earned free time cannot pause this window (exam nights)
}

type Occurrence struct {
	Window Window    `json:"window"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
}

// Key identifies one occurrence (used to remember "ended early").
func (o Occurrence) Key() string { return o.Window.ID + "|" + o.Start.UTC().Format(time.RFC3339) }

func parseHM(s string) (h, m int, err error) {
	if _, err = fmt.Sscanf(s, "%d:%d", &h, &m); err != nil || h < 0 || h > 23 || m < 0 || m > 59 || len(s) != 5 {
		return 0, 0, fmt.Errorf("bad time %q (want HH:MM)", s)
	}
	return h, m, nil
}

func (w Window) Validate() error {
	if w.ID == "" {
		return errors.New("window needs an id")
	}
	sh, sm, err := parseHM(w.Start)
	if err != nil {
		return err
	}
	eh, em, err := parseHM(w.End)
	if err != nil {
		return err
	}
	if sh == eh && sm == em {
		return errors.New("start and end must differ")
	}
	switch {
	case len(w.Days) > 0 && w.Date != "":
		return errors.New("a window is either recurring or one-off, not both")
	case len(w.Days) > 0:
		for _, d := range w.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("bad weekday %d", d)
			}
		}
	case w.Date != "":
		if _, err := time.Parse("2006-01-02", w.Date); err != nil {
			return fmt.Errorf("bad date %q", w.Date)
		}
	default:
		return errors.New("pick weekdays or a date")
	}
	return nil
}

// on returns the occurrence that begins on the calendar day y-m-d (in loc).
func (w Window) on(y int, mo time.Month, d int, loc *time.Location) (Occurrence, bool) {
	if w.Disabled {
		return Occurrence{}, false
	}
	day := time.Date(y, mo, d, 12, 0, 0, 0, loc)
	if w.Date != "" {
		if day.Format("2006-01-02") != w.Date {
			return Occurrence{}, false
		}
	} else {
		hit := false
		for _, wd := range w.Days {
			hit = hit || wd == int(day.Weekday())
		}
		if !hit {
			return Occurrence{}, false
		}
	}
	sh, sm, err1 := parseHM(w.Start)
	eh, em, err2 := parseHM(w.End)
	if err1 != nil || err2 != nil {
		return Occurrence{}, false
	}
	start := time.Date(y, mo, d, sh, sm, 0, 0, loc)
	endDay := d
	if eh*60+em <= sh*60+sm {
		endDay++
	}
	end := time.Date(y, mo, endDay, eh, em, 0, 0, loc)
	if !end.After(start) { // DST gap collapsed the interval
		end = start.Add(time.Minute)
	}
	return Occurrence{Window: w, Start: start, End: end}, true
}

// Occurrences lists every occurrence overlapping [from, to), earliest first.
func Occurrences(ws []Window, from, to time.Time, loc *time.Location) []Occurrence {
	var out []Occurrence
	first := from.In(loc).AddDate(0, 0, -2)
	for day := time.Date(first.Year(), first.Month(), first.Day(), 12, 0, 0, 0, loc); !day.After(to.In(loc).AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		for _, w := range ws {
			if o, ok := w.on(day.Year(), day.Month(), day.Day(), loc); ok && o.Start.Before(to) && o.End.After(from) {
				out = append(out, o)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// Active returns the occurrences in force at now, skipping any ended early.
func Active(ws []Window, now time.Time, loc *time.Location, ended map[string]bool) []Occurrence {
	var out []Occurrence
	for _, o := range Occurrences(ws, now, now.Add(time.Nanosecond), loc) {
		if !o.Start.After(now) && o.End.After(now) && !ended[o.Key()] {
			out = append(out, o)
		}
	}
	return out
}

// Next returns the first occurrence that starts after now (within 8 days).
func Next(ws []Window, now time.Time, loc *time.Location, ended map[string]bool) (Occurrence, bool) {
	for _, o := range Occurrences(ws, now, now.AddDate(0, 0, 8), loc) {
		if o.Start.After(now) && !ended[o.Key()] {
			return o, true
		}
	}
	return Occurrence{}, false
}

// BlockedUntil is when continuous blocking that is active at now finally
// stops, following overlapping and back-to-back windows. Zero if not active.
func BlockedUntil(ws []Window, now time.Time, loc *time.Location, ended map[string]bool) time.Time {
	occs := Occurrences(ws, now, now.AddDate(0, 0, 3), loc)
	cur := now
	for moved := true; moved; {
		moved = false
		for _, o := range occs {
			if !o.Start.After(cur) && o.End.After(cur) && !ended[o.Key()] {
				cur, moved = o.End, true
			}
		}
	}
	if cur.Equal(now) {
		return time.Time{}
	}
	return cur
}

// Resolve unions the lists of the active windows. Windows without their own
// lists contribute the global ones.
func Resolve(active []Occurrence, globalSites, globalApps []string) (sites, apps []string) {
	seenS, seenA := map[string]bool{}, map[string]bool{}
	add := func(dst *[]string, seen map[string]bool, items []string) {
		for _, s := range items {
			if !seen[s] {
				seen[s] = true
				*dst = append(*dst, s)
			}
		}
	}
	for _, o := range active {
		if o.Window.OwnLists {
			add(&sites, seenS, o.Window.Sites)
			add(&apps, seenA, o.Window.Apps)
		} else {
			add(&sites, seenS, globalSites)
			add(&apps, seenA, globalApps)
		}
	}
	return sites, apps
}
