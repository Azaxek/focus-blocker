package updater

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// CheckEvery is the automatic check interval.
const CheckEvery = time.Hour

// Manager runs the check / download / apply cycle inside the service.
type Manager struct {
	U *Updater
	// Auto reports whether automatic updates are enabled.
	Auto func() bool
	// Idle reports whether it is safe to restart the service: no window is
	// active and none is about to start. An update never opens a protection gap.
	Idle func() bool
	// Apply launches the installer helper for a staged update.
	Apply func(installer string, rel *Release) error
	// Log writes to the activity log; Record remembers the last check.
	Log    func(kind, msg string)
	Record func(t time.Time, result string)

	mu       sync.Mutex
	ready    *Release
	path     string
	applying bool
}

// Ready returns the staged version waiting for free time, or "".
func (m *Manager) Ready() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready == nil {
		return ""
	}
	return m.ready.Version
}

// CheckNow checks, and if there is a newer release downloads and verifies it.
func (m *Manager) CheckNow(ctx context.Context) (string, error) {
	rel, err := m.U.Check(ctx)
	if err == nil && rel != nil {
		m.mu.Lock()
		have := m.ready != nil && m.ready.Version == rel.Version
		m.mu.Unlock()
		if !have {
			var path string
			if path, err = m.U.Download(ctx, rel); err == nil {
				m.mu.Lock()
				m.ready, m.path = rel, path
				m.mu.Unlock()
				m.Log("update", fmt.Sprintf("Version %s downloaded and verified; installs when no focus window is active", rel.Version))
			}
		}
	}
	msg := "Up to date (" + m.U.Current + ")"
	switch {
	case err != nil:
		msg = "Check failed: " + err.Error()
		m.Log("update", msg)
	case m.Ready() != "":
		msg = "Version " + m.Ready() + " is ready and will install when no focus window is active"
	}
	m.Record(time.Now(), msg)
	return msg, err
}

// InstallNow checks, downloads and installs immediately, unless a focus window
// is active or about to start (an update never opens a protection gap).
func (m *Manager) InstallNow(ctx context.Context) (string, error) {
	if m.Ready() == "" {
		if _, err := m.CheckNow(ctx); err != nil {
			return "", err
		}
	}
	m.mu.Lock()
	rel, path, busy := m.ready, m.path, m.applying
	m.mu.Unlock()
	switch {
	case rel == nil:
		return "Already up to date (" + m.U.Current + ")", nil
	case busy:
		return "Version " + rel.Version + " is already installing", nil
	case !m.Idle():
		return "", fmt.Errorf("version %s is ready, but a focus window is active or about to start; it installs by itself when the window ends", rel.Version)
	}
	m.mu.Lock()
	m.applying = true
	m.mu.Unlock()
	m.Log("update", "Installing version "+rel.Version+" (requested by a parent)")
	if err := m.Apply(path, rel); err != nil {
		m.mu.Lock()
		m.applying = false
		m.mu.Unlock()
		return "", err
	}
	return "Installing version " + rel.Version + ". The service restarts for a few seconds.", nil
}

// Run checks on a schedule and applies a staged update as soon as it is safe.
func (m *Manager) Run(ctx context.Context) {
	next := time.Now().Add(20 * time.Second)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !m.Auto() {
			continue
		}
		if time.Now().After(next) {
			c, cancel := context.WithTimeout(ctx, 20*time.Minute)
			_, err := m.CheckNow(c)
			cancel()
			next = time.Now().Add(CheckEvery)
			if err != nil {
				next = time.Now().Add(15 * time.Minute)
			}
		}
		m.mu.Lock()
		rel, path, busy := m.ready, m.path, m.applying
		m.mu.Unlock()
		if rel != nil && !busy && m.Idle() {
			m.mu.Lock()
			m.applying = true
			m.mu.Unlock()
			m.Log("update", "Installing version "+rel.Version)
			if err := m.Apply(path, rel); err != nil {
				m.Log("update", "Could not start the installer: "+err.Error())
				m.mu.Lock()
				m.applying = false
				m.mu.Unlock()
			}
		}
	}
}
