// Package clock provides a tamper-resistant time source.
//
// trusted = wall clock + offset. The offset absorbs any wall-clock jump that
// the monotonic clock does not corroborate (a user moving the clock to end a
// window early) and is corrected against internet time when reachable.
package clock

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

const (
	// JumpThreshold is the wall-vs-monotonic disagreement treated as tampering.
	JumpThreshold = 5 * time.Second
	// SyncThreshold is the trusted-vs-internet disagreement that triggers a correction.
	SyncThreshold = 5 * time.Second
	// LargeSkew is the disagreement worth a tamper log entry on sync.
	LargeSkew = 30 * time.Second
	// ResumeGrace is how long after a power-resume a wall jump is believed
	// (sleeping can legitimately make the wall clock outrun the monotonic one).
	ResumeGrace = 90 * time.Second
)

var timeURLs = []string{"https://www.cloudflare.com", "https://www.google.com", "https://github.com"}

// FetchFunc returns the current internet time; trusted supplies the time used
// for certificate validation so a forged wall clock cannot break TLS.
type FetchFunc func(ctx context.Context, trusted func() time.Time) (time.Time, error)

type Clock struct {
	// OnTamper is called (without the lock held) when a clock anomaly is found.
	OnTamper func(msg string)
	// Fetch is the internet time source; replaceable for tests.
	Fetch FetchFunc

	mu          sync.Mutex
	wall        func() time.Time
	mono        func() time.Duration
	offset      time.Duration
	lastWall    time.Time
	lastMono    time.Duration
	acceptUntil time.Duration
	resumed     bool
	synced      bool
	lastSync    time.Time
}

func New() *Clock {
	start := time.Now()
	return NewWith(func() time.Time { return time.Now().Round(0) }, func() time.Duration { return time.Since(start) })
}

// NewWith builds a clock from explicit wall and monotonic sources (tests).
func NewWith(wall func() time.Time, mono func() time.Duration) *Clock {
	c := &Clock{wall: wall, mono: mono, Fetch: fetchHTTPDate}
	c.lastWall, c.lastMono = wall(), mono()
	return c
}

// observe folds the current readings into the offset. Caller holds mu.
func (c *Clock) observe() (time.Time, string) {
	w, m := c.wall(), c.mono()
	var msg string
	if d := w.Sub(c.lastWall) - (m - c.lastMono); d > JumpThreshold || d < -JumpThreshold {
		if c.resumed && m <= c.acceptUntil {
			c.resumed = false
		} else {
			c.offset -= d
			msg = fmt.Sprintf("system clock jumped by %s; jump ignored", d.Round(time.Second))
		}
	}
	c.lastWall, c.lastMono = w, m
	return w, msg
}

// Now returns trusted time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	w, msg := c.observe()
	t := w.Add(c.offset)
	c.mu.Unlock()
	c.tamper(msg)
	return t
}

// Resumed tells the clock the machine just woke from sleep.
func (c *Clock) Resumed() {
	c.mu.Lock()
	c.resumed, c.acceptUntil = true, c.mono()+ResumeGrace
	c.mu.Unlock()
}

// Synced reports whether internet time has been reached and when.
func (c *Clock) Synced() (bool, time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.synced, c.lastSync
}

// Sync checks trusted time against the internet and corrects the offset.
// An unreachable internet is an error but leaves the local clock in charge.
func (c *Clock) Sync(ctx context.Context) error {
	net, err := c.Fetch(ctx, c.Now)
	if err != nil {
		return err
	}
	c.mu.Lock()
	w, msg := c.observe()
	d := net.Sub(w.Add(c.offset))
	if d > SyncThreshold || d < -SyncThreshold {
		c.offset += d
		if d > LargeSkew || d < -LargeSkew {
			msg = fmt.Sprintf("system clock was %s off internet time; corrected", (-d).Round(time.Second))
		}
	}
	c.synced, c.lastSync = true, w.Add(c.offset)
	c.mu.Unlock()
	c.tamper(msg)
	return nil
}

func (c *Clock) tamper(msg string) {
	if msg != "" && c.OnTamper != nil {
		c.OnTamper(msg)
	}
}

// fetchHTTPDate takes the median Date header of several HTTPS hosts.
func fetchHTTPDate(ctx context.Context, trusted func() time.Time) (time.Time, error) {
	client := &http.Client{
		Timeout: 6 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // verified below against trusted time, not the (possibly forged) wall clock
			VerifyConnection: func(cs tls.ConnectionState) error {
				if len(cs.PeerCertificates) == 0 {
					return errors.New("no certificate")
				}
				pool := x509.NewCertPool()
				for _, ic := range cs.PeerCertificates[1:] {
					pool.AddCert(ic)
				}
				_, err := cs.PeerCertificates[0].Verify(x509.VerifyOptions{DNSName: cs.ServerName, Intermediates: pool, CurrentTime: trusted()})
				return err
			},
		}},
	}
	type res struct {
		t   time.Time
		err error
	}
	ch := make(chan res, len(timeURLs))
	for _, u := range timeURLs {
		go func(u string) {
			req, _ := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
			resp, err := client.Do(req)
			if err != nil {
				ch <- res{err: err}
				return
			}
			resp.Body.Close()
			t, err := http.ParseTime(resp.Header.Get("Date"))
			// the header is stamped when the server answered; add half a second for transit
			ch <- res{t: t.Add(500 * time.Millisecond), err: err}
		}(u)
	}
	var ts []time.Time
	var last error
	for range timeURLs {
		r := <-ch
		if r.err != nil {
			last = r.err
			continue
		}
		ts = append(ts, r.t)
	}
	if len(ts) == 0 {
		return time.Time{}, fmt.Errorf("no time source reachable: %w", last)
	}
	sort.Slice(ts, func(i, j int) bool { return ts[i].Before(ts[j]) })
	return ts[len(ts)/2], nil
}
