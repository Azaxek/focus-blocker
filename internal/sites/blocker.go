package sites

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Blocker keeps the OS in the state "these domains are blocked" and repairs
// it when something changes it behind our back.
type Blocker struct {
	Sink  *Sinkhole
	hosts string

	mu           sync.Mutex
	applied      string // fingerprint of the last applied set; "?" = unknown, clean up first
	appliedAt    time.Time
	scriptAt     time.Time
	scriptFailed bool
}

func NewBlocker(sink *Sinkhole) *Blocker {
	return &Blocker{Sink: sink, hosts: HostsPath(), applied: "?"}
}

// Reconcile drives the system towards blocking exactly domains until `until`.
// It returns the components it had to restore (non-empty means something was
// tampered with, or failed, since the last call).
func (b *Blocker) Reconcile(domains []string, until time.Time) (repaired []string, err error) {
	var keep []string
	for _, d := range domains {
		if !Protected(d) {
			keep = append(keep, d)
		}
	}
	domains = keep
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Sink.Set(domains, until)
	fp := strings.Join(domains, ",")

	if len(domains) == 0 {
		if b.applied == "" {
			return nil, nil
		}
		err = b.clear()
		if err == nil {
			b.applied = ""
		}
		return nil, err
	}
	var errs []error
	if !b.Sink.Running() {
		if e := b.Sink.Start(); e != nil {
			errs = append(errs, e) // reported once; hosts + firewall + policies still protect without it
		}
	}
	if b.applied != fp {
		errs = append(errs, b.fix(domains, "hosts", "policy", "nrpt", "firewall"))
		b.applied, b.appliedAt = fp, time.Now()
		flushDNS()
		return nil, errors.Join(errs...)
	}
	bad := b.verify(domains)
	if len(bad) > 0 {
		errs = append(errs, b.fix(domains, bad...))
		flushDNS()
	}
	return bad, errors.Join(errs...)
}

func (b *Blocker) verify(domains []string) (bad []string) {
	if !hostsIntact(b.hosts, domains) {
		bad = append(bad, "hosts")
	}
	if !policiesOK() || !windowPoliciesOK() {
		bad = append(bad, "policy")
	}
	if countFirewallRules() < firewallRuleCount {
		bad = append(bad, "firewall")
	}
	if b.Sink.Running() && time.Since(b.appliedAt) > 15*time.Second && !probeNRPT(domains[0]) { // grace: NRPT takes a moment to bite
		bad = append(bad, "nrpt")
	}
	return bad
}

func (b *Blocker) fix(domains []string, parts ...string) error {
	var errs []error
	var script string
	for _, p := range parts {
		switch p {
		case "hosts":
			errs = append(errs, writeHosts(b.hosts, domains))
		case "policy":
			errs = append(errs, setPolicies(), setWindowPolicies())
		case "nrpt":
			script += nrptScript(domains)
		case "firewall":
			script += firewallScript(true)
		}
	}
	if script != "" {
		// a failing PowerShell script is retried at most every 30 seconds, not every pass
		if b.scriptFailed && time.Since(b.scriptAt) < 30*time.Second {
			errs = append(errs, errors.New("waiting to retry the DNS/firewall rules"))
		} else {
			err := runPS(script)
			b.scriptAt, b.scriptFailed = time.Now(), err != nil
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// clear removes every per-window rule, including the "connect directly" browser
// policy. The permanent policies (encrypted DNS and QUIC off) stay: they are harmless.
func (b *Blocker) clear() error {
	b.Sink.Stop()
	removeWindowPolicies()
	err := writeHosts(b.hosts, nil)
	if b.scriptFailed && time.Since(b.scriptAt) < 30*time.Second {
		return errors.Join(err, errors.New("waiting to retry removing the DNS/firewall rules"))
	}
	psErr := runPS(nrptScript(nil) + firewallScript(false))
	b.scriptAt, b.scriptFailed = time.Now(), psErr != nil
	flushDNS()
	return errors.Join(err, psErr)
}

// Install applies the permanent protections (browser policies) at service start.
func (b *Blocker) Install() error { return setPolicies() }

// Uninstall removes everything this package ever added.
func (b *Blocker) Uninstall() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	err := b.clear()
	removePolicies()
	removeWindowPolicies()
	b.applied = ""
	return err
}
