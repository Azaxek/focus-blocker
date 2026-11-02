package schedule

import (
	"reflect"
	"testing"
	"time"
)

func chicago(t *testing.T) *time.Location {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skip("tzdata unavailable: ", err)
	}
	return loc
}

func at(loc *time.Location, y int, mo time.Month, d, h, m int) time.Time {
	return time.Date(y, mo, d, h, m, 0, 0, loc)
}

func win(id string, days []int, start, end string) Window {
	return Window{ID: id, Name: id, Days: days, Start: start, End: end}
}

// 2026-05-04 is a Monday.
func TestWeekdayWindow(t *testing.T) {
	loc := chicago(t)
	ws := []Window{win("school", []int{1, 2, 3, 4, 5}, "15:30", "21:00")}
	cases := []struct {
		when   time.Time
		active bool
	}{
		{at(loc, 2026, 5, 4, 15, 29), false},
		{at(loc, 2026, 5, 4, 15, 30), true},
		{at(loc, 2026, 5, 4, 20, 59), true},
		{at(loc, 2026, 5, 4, 21, 0), false},
		{at(loc, 2026, 5, 9, 18, 0), false}, // Saturday
	}
	for _, c := range cases {
		if got := len(Active(ws, c.when, loc, nil)) == 1; got != c.active {
			t.Errorf("%v: active=%v want %v", c.when, got, c.active)
		}
	}
}

func TestMidnightCrossover(t *testing.T) {
	loc := chicago(t)
	ws := []Window{win("late", []int{5}, "22:00", "02:00")} // Fridays 22:00 -> Saturday 02:00
	for _, c := range []struct {
		when   time.Time
		active bool
	}{
		{at(loc, 2026, 5, 8, 21, 59), false},
		{at(loc, 2026, 5, 8, 23, 59), true},
		{at(loc, 2026, 5, 9, 0, 0), true},
		{at(loc, 2026, 5, 9, 1, 59), true},
		{at(loc, 2026, 5, 9, 2, 0), false},
		{at(loc, 2026, 5, 10, 1, 0), false}, // Sunday early morning: Saturday has no window
	} {
		if got := len(Active(ws, c.when, loc, nil)) == 1; got != c.active {
			t.Errorf("%v: active=%v want %v", c.when, got, c.active)
		}
	}
}

func TestEndOfDayMidnight(t *testing.T) {
	loc := chicago(t)
	ws := []Window{win("w", []int{1}, "20:00", "00:00")}
	if len(Active(ws, at(loc, 2026, 5, 4, 23, 59), loc, nil)) != 1 || len(Active(ws, at(loc, 2026, 5, 5, 0, 0), loc, nil)) != 0 {
		t.Fatal("00:00 end should stop exactly at midnight")
	}
}

func TestDSTSpringForward(t *testing.T) {
	loc := chicago(t)
	// 2026-03-08 (Sunday): 02:00 jumps to 03:00 in America/Chicago.
	ws := []Window{win("dst", []int{0}, "01:00", "04:00")}
	if len(Active(ws, at(loc, 2026, 3, 8, 1, 30), loc, nil)) != 1 {
		t.Error("01:30 CST should be active")
	}
	if len(Active(ws, at(loc, 2026, 3, 8, 3, 30), loc, nil)) != 1 {
		t.Error("03:30 CDT should be active")
	}
	if len(Active(ws, at(loc, 2026, 3, 8, 4, 0), loc, nil)) != 0 {
		t.Error("04:00 CDT should be over")
	}
	o := Occurrences(ws, at(loc, 2026, 3, 8, 0, 0), at(loc, 2026, 3, 9, 0, 0), loc)
	if len(o) != 1 || o[0].End.Sub(o[0].Start) != 2*time.Hour {
		t.Errorf("window should be 2 real hours that night, got %v", o)
	}
}

func TestDSTFallBack(t *testing.T) {
	loc := chicago(t)
	// 2026-11-01 (Sunday): 02:00 CDT falls back to 01:00 CST, so 01:30 happens twice.
	ws := []Window{win("dst", []int{0}, "00:30", "03:00")}
	first := time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC)  // 01:30 CDT
	second := time.Date(2026, 11, 1, 7, 30, 0, 0, time.UTC) // 01:30 CST
	end := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)     // 03:00 CST
	for _, tm := range []time.Time{first, second} {
		if len(Active(ws, tm, loc, nil)) != 1 {
			t.Errorf("%v should be active", tm)
		}
	}
	if len(Active(ws, end, loc, nil)) != 0 {
		t.Error("03:00 CST should be over")
	}
}

func TestOneOffAndDisabled(t *testing.T) {
	loc := chicago(t)
	ws := []Window{
		{ID: "once", Name: "exam", Date: "2026-05-06", Start: "09:00", End: "12:00"},
		{ID: "off", Name: "off", Days: []int{3}, Start: "09:00", End: "12:00", Disabled: true},
	}
	if len(Active(ws, at(loc, 2026, 5, 6, 10, 0), loc, nil)) != 1 {
		t.Error("one-off should be active on its date")
	}
	if len(Active(ws, at(loc, 2026, 5, 13, 10, 0), loc, nil)) != 0 {
		t.Error("one-off must not recur, disabled must not fire")
	}
}

func TestOverlapAndBlockedUntil(t *testing.T) {
	loc := chicago(t)
	ws := []Window{
		win("a", []int{1}, "15:00", "18:00"),
		win("b", []int{1}, "17:00", "20:00"),
		win("c", []int{1}, "20:00", "21:00"), // back to back with b
	}
	now := at(loc, 2026, 5, 4, 17, 30)
	if n := len(Active(ws, now, loc, nil)); n != 2 {
		t.Fatalf("want 2 overlapping, got %d", n)
	}
	if got := BlockedUntil(ws, now, loc, nil); !got.Equal(at(loc, 2026, 5, 4, 21, 0)) {
		t.Fatalf("blocked until %v", got)
	}
	if !BlockedUntil(ws, at(loc, 2026, 5, 4, 22, 0), loc, nil).IsZero() {
		t.Fatal("not blocked at 22:00")
	}
}

func TestEndedEarlyOnlyAffectsThatOccurrence(t *testing.T) {
	loc := chicago(t)
	ws := []Window{win("a", []int{1, 2}, "15:00", "18:00")}
	now := at(loc, 2026, 5, 4, 16, 0)
	occ := Active(ws, now, loc, nil)[0]
	ended := map[string]bool{occ.Key(): true}
	if len(Active(ws, now, loc, ended)) != 0 {
		t.Fatal("ended occurrence still active")
	}
	if len(Active(ws, at(loc, 2026, 5, 5, 16, 0), loc, ended)) != 1 {
		t.Fatal("next day must still be enforced")
	}
	if n, ok := Next(ws, now, loc, ended); !ok || !n.Start.Equal(at(loc, 2026, 5, 5, 15, 0)) {
		t.Fatalf("next = %v %v", n, ok)
	}
}

func TestResolveGlobalVersusOwn(t *testing.T) {
	g := Window{ID: "g"}
	o := Window{ID: "o", OwnLists: true, Sites: []string{"b.com", "a.com"}, Apps: []string{"x"}}
	sites, apps := Resolve([]Occurrence{{Window: g}, {Window: o}}, []string{"a.com", "c.com"}, []string{"steam"})
	if !reflect.DeepEqual(sites, []string{"a.com", "c.com", "b.com"}) || !reflect.DeepEqual(apps, []string{"steam", "x"}) {
		t.Fatalf("sites=%v apps=%v", sites, apps)
	}
}

func TestValidate(t *testing.T) {
	bad := []Window{
		{ID: "", Days: []int{1}, Start: "09:00", End: "10:00"},
		{ID: "a", Days: []int{1}, Start: "9:00", End: "10:00"},
		{ID: "a", Days: []int{1}, Start: "09:00", End: "09:00"},
		{ID: "a", Days: []int{7}, Start: "09:00", End: "10:00"},
		{ID: "a", Start: "09:00", End: "10:00"},
		{ID: "a", Days: []int{1}, Date: "2026-01-01", Start: "09:00", End: "10:00"},
	}
	for i, w := range bad {
		if w.Validate() == nil {
			t.Errorf("case %d should be invalid", i)
		}
	}
	if err := win("ok", []int{1}, "09:00", "10:00").Validate(); err != nil {
		t.Error(err)
	}
}
