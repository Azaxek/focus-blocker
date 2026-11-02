package clock

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fake struct {
	wall time.Time
	mono time.Duration
}

func (f *fake) advance(d time.Duration) { f.wall, f.mono = f.wall.Add(d), f.mono+d }
func (f *fake) setWall(t time.Time)     { f.wall = t }
func (f *fake) clock() (*Clock, *[]string) {
	var log []string
	c := NewWith(func() time.Time { return f.wall }, func() time.Duration { return f.mono })
	c.OnTamper = func(m string) { log = append(log, m) }
	return c, &log
}

var t0 = time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC)

func TestNormalTimePasses(t *testing.T) {
	f := &fake{wall: t0}
	c, log := f.clock()
	f.advance(3 * time.Minute)
	if got := c.Now(); !got.Equal(t0.Add(3*time.Minute)) || len(*log) != 0 {
		t.Fatalf("got %v log %v", got, *log)
	}
}

func TestForwardJumpIsIgnored(t *testing.T) {
	f := &fake{wall: t0}
	c, log := f.clock()
	f.advance(time.Minute)
	f.setWall(f.wall.Add(5 * time.Hour)) // user sets the clock forward to skip a window
	got := c.Now()
	if !got.Equal(t0.Add(time.Minute)) {
		t.Fatalf("trusted time followed the forged clock: %v", got)
	}
	if len(*log) != 1 {
		t.Fatalf("expected one tamper event, got %v", *log)
	}
	f.advance(time.Minute) // time keeps flowing normally afterwards
	if got := c.Now(); !got.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("got %v", got)
	}
}

func TestBackwardJumpIsIgnored(t *testing.T) {
	f := &fake{wall: t0}
	c, log := f.clock()
	f.setWall(t0.Add(-2 * time.Hour))
	if got := c.Now(); !got.Equal(t0) || len(*log) != 1 {
		t.Fatalf("got %v log %v", got, *log)
	}
}

func TestResumeAcceptsOneJump(t *testing.T) {
	f := &fake{wall: t0}
	c, log := f.clock()
	// sleep for 2h: the monotonic clock did not move, the wall clock did
	f.wall = f.wall.Add(2 * time.Hour)
	c.Resumed()
	if got := c.Now(); !got.Equal(t0.Add(2*time.Hour)) || len(*log) != 0 {
		t.Fatalf("resume jump not accepted: %v %v", got, *log)
	}
	f.setWall(f.wall.Add(time.Hour)) // a second jump is tampering
	c.Now()
	if len(*log) != 1 {
		t.Fatalf("second jump should be tamper, log %v", *log)
	}
}

func TestSyncCorrectsOffsetAndLogsLargeSkew(t *testing.T) {
	f := &fake{wall: t0.Add(3 * time.Hour)} // clock already wrong at boot
	c, log := f.clock()
	c.Fetch = func(context.Context, func() time.Time) (time.Time, error) { return t0, nil }
	if err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.Now(); got.Sub(t0) > time.Second || len(*log) != 1 {
		t.Fatalf("got %v log %v", got, *log)
	}
	if ok, _ := c.Synced(); !ok {
		t.Fatal("not marked synced")
	}
}

func TestOfflineFallsBackToLocal(t *testing.T) {
	f := &fake{wall: t0}
	c, _ := f.clock()
	c.Fetch = func(context.Context, func() time.Time) (time.Time, error) { return time.Time{}, errors.New("offline") }
	if err := c.Sync(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if got := c.Now(); !got.Equal(t0) {
		t.Fatalf("got %v", got)
	}
	if ok, _ := c.Synced(); ok {
		t.Fatal("must not be marked synced")
	}
}
