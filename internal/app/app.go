// Package app wires the engine, blockers, auth, updater and dashboard into
// one running service.
package app

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/auth"
	"github.com/Azaxek/focus-blocker/internal/clock"
	"github.com/Azaxek/focus-blocker/internal/engine"
	"github.com/Azaxek/focus-blocker/internal/fsx"
	"github.com/Azaxek/focus-blocker/internal/server"
	"github.com/Azaxek/focus-blocker/internal/sites"
	"github.com/Azaxek/focus-blocker/internal/store"
	"github.com/Azaxek/focus-blocker/internal/updater"
)

type Options struct {
	DataDir string
	Version string
	Repo    string            // GitHub "owner/name" updates come from
	PubKey  ed25519.PublicKey // release signing key
	Dry     bool              // console/dev: touch nothing on the machine
}

// DataDir is where the passcode hash, settings and logs live.
func DataDir() string {
	if d := os.Getenv("FOCUSGUARD_DATA"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("ProgramData"), "FocusGuard")
}

type App struct {
	Eng    *engine.Engine
	opts   Options
	cancel context.CancelFunc
	mu     sync.Mutex
	clean  bool
}

// Resume must be called when the machine wakes from sleep.
func (a *App) Resume() {
	if a != nil && a.Eng != nil {
		a.Eng.Resumed()
	}
}

// Run blocks until ctx is cancelled or a passcode-approved stop.
func Run(ctx context.Context, o Options, ready func(*App)) error {
	if err := os.MkdirAll(o.DataDir, 0o700); err != nil {
		return err
	}
	// A service that refuses to start blocks nothing, so problems below are logged and the
	// service keeps enforcing: it is better to run with a weaker lock than not to run.
	var lockErr error
	if !o.Dry {
		lockErr = fsx.Lock(o.DataDir)
	}
	st, err := store.Open(o.DataDir)
	if err != nil {
		return err
	}
	clk := clock.New()
	if lockErr != nil {
		st.Log(clk.Now(), "error", "could not restrict the data folder to administrators: "+lockErr.Error())
	}
	au := auth.New(filepath.Join(o.DataDir, "auth.json"), clk.Now)

	var siteB engine.SiteBlocker
	var appB engine.AppBlocker
	var sink *sites.Sinkhole
	var procs func() []string
	if o.Dry {
		siteB, appB = dryRun{}, dryRun{}
	} else {
		sink = sites.NewSinkhole()
		blocker := sites.NewBlocker(sink)
		if err := blocker.Install(); err != nil {
			st.Log(clk.Now(), "error", "could not set browser policies: "+err.Error())
		}
		siteB = blocker
		enf := apps.NewEnforcer(&apps.WinSystem{}, uint32(os.Getpid()))
		appB = enf
		sys := &apps.WinSystem{}
		procs = func() []string {
			list, _ := sys.Procs()
			seen := map[string]bool{}
			var out []string
			for _, p := range list {
				if !seen[p.Name] && !apps.Protected(p.Name) && p.PID > 4 {
					seen[p.Name] = true
					out = append(out, p.Name)
				}
			}
			return out
		}
	}

	eng := engine.New(o.Version, st, clk, siteB, appB)
	clk.OnTamper = eng.Tamper
	if sink != nil {
		sink.OnAttempt = eng.SiteAttempt
	}

	if r := updater.ConsumeResult(o.DataDir); r != nil {
		st.Log(clk.Now(), "update", r.Message)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	a := &App{Eng: eng, opts: o, cancel: cancel}

	srv := server.New(o.Version)
	srv.Eng, srv.Auth, srv.Store, srv.Procs = eng, au, st, procs
	srv.Stop = func() {
		a.mu.Lock()
		a.clean = true
		a.mu.Unlock()
		cancel()
	}

	if !o.Dry && o.Repo != "" {
		exe, _ := os.Executable()
		upd := &updater.Updater{Repo: o.Repo, PubKey: o.PubKey, Current: o.Version, Dir: filepath.Join(o.DataDir, "updates")}
		mgr := &updater.Manager{
			U:    upd,
			Auto: func() bool { return st.Config().AutoUpdate },
			Idle: func() bool {
				s := eng.Status()
				return !s.Blocking && (s.Next == nil || s.Next.Start.Sub(s.Now) > 5*time.Minute)
			},
			Apply: func(installer string, rel *updater.Release) error {
				return LaunchUpdateHelper(exe, installer, o.Version, rel.Version, o.DataDir)
			},
			Log: func(kind, msg string) { st.Log(clk.Now(), kind, msg) },
			Record: func(t time.Time, msg string) {
				st.UpdateState(func(s *store.State) { s.LastCheck, s.LastCheckResult = t, msg })
			},
		}
		srv.Upd = mgr
		go func() {
			defer recoverLog(st, clk, "updater")
			mgr.Run(ctx)
		}()
	}

	// The dashboard is not what enforces anything, so a failure to open its port (another
	// program got there first) must not stop the service: keep trying while the engine works.
	go serveDashboard(ctx, srv, st, clk)
	go eng.Run(ctx)
	st.Log(clk.Now(), "service", "Service started, version "+o.Version)
	if ready != nil {
		ready(a)
	}

	<-ctx.Done()
	a.mu.Lock()
	clean := a.clean
	a.mu.Unlock()
	if clean {
		eng.Shutdown()
	}
	return nil
}

// retryEvery is how long the dashboard waits before trying its port again.
var retryEvery = 10 * time.Second

func serveDashboard(ctx context.Context, srv *server.Server, st *store.Store, clk *clock.Clock) {
	var lastLog time.Time
	for ctx.Err() == nil {
		err := srv.ListenAndServe(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}
		if time.Since(lastLog) > 10*time.Minute { // the log would fill up otherwise
			st.Log(clk.Now(), "error", "the dashboard could not start ("+err.Error()+"); blocking continues and it will keep trying")
			lastLog = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retryEvery):
		}
	}
}

func recoverLog(st *store.Store, clk *clock.Clock, what string) {
	if r := recover(); r != nil {
		st.Log(clk.Now(), "error", fmt.Sprintf("internal error in %s (recovered): %v", what, r))
	}
}

// dryRun is the blocker used in console/dev mode: it only reports.
type dryRun struct{}

func (dryRun) Reconcile(d []string, u time.Time) ([]string, error) { return nil, nil }
func (dryRun) SetRules([]apps.Rule)                                {}
func (dryRun) Scan() []apps.Killed                                 { return nil }
