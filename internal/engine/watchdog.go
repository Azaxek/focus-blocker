package engine

import (
	"context"
	"strings"
	"time"
)

// WatchdogInterval is how often enforcement is re-verified and repaired.
const WatchdogInterval = 3 * time.Second

// siteLoop is the watchdog: each pass makes the machine match the wanted
// state, which also repairs hosts, DNS, firewall or policy edits made behind
// our back. Anything it had to restore is logged as a tamper attempt.
func (e *Engine) siteLoop(ctx context.Context) {
	t := time.NewTicker(WatchdogInterval)
	defer t.Stop()
	for {
		e.mu.Lock()
		w := e.want
		e.mu.Unlock()
		repaired, err := e.Sites.Reconcile(w.domains, w.until)
		if len(repaired) > 0 {
			e.Tamper("Website block was altered (" + strings.Join(repaired, ", ") + "); restored")
		}
		e.setProblem("site blocking", err)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.siteWake:
		}
	}
}

// setProblem keeps the latest enforcement problem for the dashboard and
// logs each distinct one at most once a minute.
func (e *Engine) setProblem(area string, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err == nil {
		e.problem = ""
		return
	}
	msg := area + ": " + err.Error()
	e.problem = msg
	if time.Since(e.lastErr[msg]) > time.Minute {
		e.lastErr[msg] = time.Now()
		go e.Store.Log(e.Clock.Now(), "error", msg)
	}
}

// Resumed is called when the machine wakes from sleep.
func (e *Engine) Resumed() {
	e.Clock.Resumed()
	select {
	case e.syncNow <- struct{}{}:
	default:
	}
	e.Wake()
}

func (e *Engine) syncLoop(ctx context.Context) {
	wait := time.Duration(0)
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		case <-e.syncNow:
		}
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		err := e.Clock.Sync(sctx)
		cancel()
		if err != nil { // offline: the local clock stays in charge
			failures++
			wait = time.Minute
			if failures < 6 {
				wait = 10 * time.Second // at boot the network is often not up yet
			}
		} else {
			failures, wait = 0, 10*time.Minute
		}
		e.Wake()
	}
}
