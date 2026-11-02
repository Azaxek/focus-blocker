package schedule

import (
	"fmt"
	"math/rand"
	"testing"
	"time"
)

func randomWindows(rng *rand.Rand, n int) []Window {
	var ws []Window
	hm := func() string { return fmt.Sprintf("%02d:%02d", rng.Intn(24), rng.Intn(12)*5) }
	for i := 0; i < n; i++ {
		w := Window{ID: fmt.Sprintf("w%d", i), Name: "w", Start: hm(), End: hm()}
		for w.End == w.Start {
			w.End = hm()
		}
		if rng.Intn(4) == 0 {
			w.Date = time.Date(2026, time.Month(1+rng.Intn(12)), 1+rng.Intn(28), 12, 0, 0, 0, time.UTC).Format("2006-01-02")
		} else {
			for d := 0; d < 7; d++ {
				if rng.Intn(3) == 0 {
					w.Days = append(w.Days, d)
				}
			}
			if len(w.Days) == 0 {
				w.Days = []int{rng.Intn(7)}
			}
		}
		w.Disabled = rng.Intn(10) == 0
		ws = append(ws, w)
	}
	return ws
}

// Instants chosen to land on and around daylight-saving changes in several zones.
func TestScheduleInvariantsAcrossZonesAndDST(t *testing.T) {
	zones := []string{"America/Chicago", "Europe/London", "Pacific/Auckland", "Asia/Kolkata", "America/Sao_Paulo", "Australia/Lord_Howe", "UTC"}
	anchors := []time.Time{
		time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 4, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC),
	}
	for zi, zn := range zones {
		loc, err := time.LoadLocation(zn)
		if err != nil {
			t.Skip("tzdata unavailable: ", err)
		}
		rng := rand.New(rand.NewSource(int64(zi + 1)))
		for round := 0; round < 60; round++ {
			ws := randomWindows(rng, 1+rng.Intn(5))
			for _, a := range anchors {
				now := a.Add(time.Duration(rng.Intn(60*60*60)) * time.Second)
				check(t, zn, ws, now, loc)
			}
		}
	}
}

func check(t *testing.T, zone string, ws []Window, now time.Time, loc *time.Location) {
	t.Helper()
	fail := func(f string, a ...any) {
		t.Fatalf("%s now=%v ws=%+v: %s", zone, now.In(loc), ws, fmt.Sprintf(f, a...))
	}
	act := Active(ws, now, loc, nil)
	until := BlockedUntil(ws, now, loc, nil)
	for _, o := range act {
		if o.Start.After(now) || !o.End.After(now) {
			fail("active occurrence %v-%v does not contain now", o.Start, o.End)
		}
		if !o.End.After(o.Start) || o.End.Sub(o.Start) > 26*time.Hour {
			fail("odd occurrence length %v", o.End.Sub(o.Start))
		}
	}
	switch {
	case len(act) > 0 && !until.After(now):
		fail("active but BlockedUntil=%v", until)
	case len(act) == 0 && !until.IsZero():
		fail("nothing active but BlockedUntil=%v", until)
	}
	for _, o := range act {
		if until.Before(o.End) {
			fail("BlockedUntil %v is before an active window's end %v", until, o.End)
		}
	}
	if nx, ok := Next(ws, now, loc, nil); ok {
		if !nx.Start.After(now) {
			fail("Next starts at or before now")
		}
		for _, o := range Occurrences(ws, now, nx.Start, loc) {
			if o.Start.After(now) && o.Start.Before(nx.Start) {
				fail("Next skipped an earlier start %v", o.Start)
			}
		}
	}
	// ending everything that is active frees the moment
	ended := map[string]bool{}
	for _, o := range act {
		ended[o.Key()] = true
	}
	if left := Active(ws, now, loc, ended); len(left) != 0 {
		fail("ended windows are still active: %v", left)
	}
	if !BlockedUntil(ws, now, loc, ended).IsZero() {
		fail("ended windows still block")
	}
	occ := Occurrences(ws, now.Add(-72*time.Hour), now.Add(72*time.Hour), loc)
	for i := 1; i < len(occ); i++ {
		if occ[i].Start.Before(occ[i-1].Start) {
			fail("occurrences out of order")
		}
	}
}

func FuzzWindowValidate(f *testing.F) {
	f.Add("15:30", "21:00", "2026-05-04")
	f.Add("", "", "")
	f.Add("24:00", "99:99", "not-a-date")
	f.Add("9:5", "09:05 ", "2026-13-40")
	f.Fuzz(func(t *testing.T, start, end, date string) {
		w := Window{ID: "x", Name: "x", Start: start, End: end, Date: date}
		if w.Validate() != nil {
			return
		}
		// a window that validates must also be usable
		loc := time.UTC
		_ = Occurrences([]Window{w}, time.Date(2026, 5, 1, 0, 0, 0, 0, loc), time.Date(2026, 5, 31, 0, 0, 0, 0, loc), loc)
		check(t, "UTC", []Window{w}, time.Date(2026, 5, 4, 15, 45, 0, 0, loc), loc)
	})
}
