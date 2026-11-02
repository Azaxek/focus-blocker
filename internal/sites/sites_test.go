package sites

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/sys/windows/registry"
)

func TestNormalize(t *testing.T) {
	good := map[string]string{
		"YouTube.com":                         "youtube.com",
		"https://www.reddit.com/r/golang?x=1": "reddit.com",
		"www.twitch.tv":                       "twitch.tv",
		"*.tiktok.com":                        "tiktok.com",
		"x.com:443/home":                      "x.com",
		"  discord.gg.  ":                     "discord.gg",
	}
	for in, want := range good {
		if got, err := Normalize(in); err != nil || got != want {
			t.Errorf("%q => %q, %v (want %q)", in, got, err, want)
		}
	}
	for _, in := range []string{"", "localhost", "127.0.0.1", "a b.com", "-bad.com", "foo..com", "http://", "[::1]"} {
		if _, err := Normalize(in); err == nil {
			t.Errorf("%q should be rejected", in)
		}
	}
}

func TestMatchCoversSubdomainsNotLookalikes(t *testing.T) {
	set := map[string]bool{"youtube.com": true}
	for name, want := range map[string]bool{
		"youtube.com": true, "www.youtube.com": true, "m.youtube.com.": true, "a.b.c.youtube.com": true,
		"notyoutube.com": false, "youtube.com.evil.org": false, "com": false,
	} {
		if _, got := match(set, name); got != want {
			t.Errorf("%s: got %v want %v", name, got, want)
		}
	}
}

func TestProtectedHosts(t *testing.T) {
	for d, want := range map[string]bool{"github.com": true, "githubusercontent.com": true, "api.github.com": true, "youtube.com": false} {
		if Protected(d) != want {
			t.Errorf("Protected(%s) != %v", d, want)
		}
	}
}

func TestHostsMergeIsIdempotentAndPreservesOtherLines(t *testing.T) {
	orig := "127.0.0.1 localhost\r\n# my own note\r\n10.0.0.5 nas.local\r\n"
	d := []string{"youtube.com", "tiktok.com"}
	once := mergeHosts(orig, d)
	if mergeHosts(once, d) != once {
		t.Fatal("merge is not idempotent")
	}
	for _, want := range []string{"127.0.0.1 localhost", "# my own note", "10.0.0.5 nas.local", "127.0.0.1 youtube.com", "::1 www.tiktok.com"} {
		if !strings.Contains(once, want) {
			t.Errorf("missing %q", want)
		}
	}
	if stripHosts(once) != orig {
		t.Fatalf("strip did not restore the original:\n%q", stripHosts(once))
	}
	if !hostsOK(once, d) || hostsOK(once, []string{"youtube.com"}) {
		t.Fatal("hostsOK wrong")
	}
	if hostsOK(strings.Replace(once, "127.0.0.1 youtube.com", "# 127.0.0.1 youtube.com", 1), d) {
		t.Fatal("tampering (commenting a line out) not detected")
	}
}

func TestWriteHostsRestoresTamperedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "hosts")
	os.WriteFile(p, []byte("127.0.0.1 localhost\r\n"), 0o644)
	d := []string{"reddit.com"}
	if err := writeHosts(p, d); err != nil || !hostsIntact(p, d) {
		t.Fatal("initial write failed")
	}
	os.WriteFile(p, []byte("127.0.0.1 localhost\r\n"), 0o644) // user deletes the block
	if hostsIntact(p, d) {
		t.Fatal("tamper not detected")
	}
	writeHosts(p, d)
	if !hostsIntact(p, d) {
		t.Fatal("not restored")
	}
	writeHosts(p, nil)
	if b, _ := os.ReadFile(p); string(b) != "127.0.0.1 localhost\r\n" {
		t.Fatalf("clear left %q", b)
	}
}

func TestScriptsNameEveryDomain(t *testing.T) {
	s := nrptScript([]string{"youtube.com"})
	for _, want := range []string{"'.youtube.com'", "'youtube.com'", canary, "127.0.0.1"} {
		if !strings.Contains(s, want) {
			t.Errorf("nrpt script missing %s", want)
		}
	}
	if strings.Contains(nrptScript(nil), "Add-DnsClientNrptRule") {
		t.Error("empty set must only remove")
	}
	fw := firewallScript(true)
	if strings.Count(fw, "New-NetFirewallRule") != firewallRuleCount || strings.Contains(firewallScript(false), "New-NetFirewallRule") {
		t.Error("firewall rule count wrong")
	}
	for _, ip := range dohIPs {
		if net.ParseIP(ip) == nil {
			t.Errorf("bad resolver ip %s", ip)
		}
	}
}

func TestPoliciesRoundTrip(t *testing.T) {
	root, base := polRoot, polBase
	defer func() { polRoot, polBase = root, base }()
	polRoot, polBase = registry.CURRENT_USER, `Software\FocusGuardTest`
	defer registry.DeleteKey(registry.CURRENT_USER, `Software\FocusGuardTest`)
	if policiesOK() {
		t.Fatal("should start unset")
	}
	if err := setPolicies(); err != nil || !policiesOK() {
		t.Fatalf("set: %v", err)
	}
	k, _ := registry.OpenKey(polRoot, polBase+`\Google\Chrome`, registry.SET_VALUE)
	k.SetDWordValue("QuicAllowed", 1) // user flips a policy back
	k.Close()
	if policiesOK() {
		t.Fatal("tamper not detected")
	}
	setPolicies()
	if !policiesOK() {
		t.Fatal("not restored")
	}
	removePolicies()
	if policiesOK() {
		t.Fatal("remove failed")
	}
}

func TestSinkholeDNSAnswers(t *testing.T) {
	s := NewSinkhole()
	var counted []string
	s.OnAttempt = func(d string) { counted = append(counted, d) }
	s.Set([]string{"youtube.com"}, time.Now().Add(time.Hour))
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &dns.Server{PacketConn: pc, Handler: s.dnsHandler()}
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go srv.ActivateAndServe()
	<-started
	defer srv.Shutdown()

	ask := func(name string, qt uint16) *dns.Msg {
		m := new(dns.Msg)
		m.SetQuestion(dns.Fqdn(name), qt)
		r, _, err := new(dns.Client).Exchange(m, pc.LocalAddr().String())
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := ask("m.youtube.com", dns.TypeA); len(r.Answer) != 1 || !r.Answer[0].(*dns.A).A.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("A: %v", r)
	}
	if r := ask("deep.sub.youtube.com", dns.TypeAAAA); len(r.Answer) != 1 {
		t.Fatalf("AAAA: %v", r)
	}
	if r := ask("youtube.com", dns.TypeHTTPS); r.Rcode != dns.RcodeSuccess || len(r.Answer) != 0 {
		t.Fatalf("HTTPS rr should be empty NOERROR: %v", r)
	}
	if r := ask(canary, dns.TypeA); r.Rcode != dns.RcodeNameError {
		t.Fatalf("canary should be NXDOMAIN: %v", r)
	}
	if r := ask("example.org", dns.TypeA); r.Rcode != dns.RcodeServerFailure {
		t.Fatalf("unlisted name must fail closed: %v", r)
	}
	if len(counted) != 1 || counted[0] != "youtube.com" { // second query is rate limited (5s)
		t.Fatalf("attempts counted %v", counted)
	}
}
