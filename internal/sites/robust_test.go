package sites

import (
	"math/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"youtube.com", "https://www.YouTube.com/watch?v=1", "*.reddit.com", "a..b", "xn--80ak6aa92e.com", "1.2.3.4", "[::1]:80", "a.b:99999", " \t", "-a.com", strings.Repeat("a.", 200)} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		d, err := Normalize(in)
		if err != nil {
			return
		}
		if d != strings.ToLower(d) || !strings.Contains(d, ".") || len(d) > 253 || strings.ContainsAny(d, " /?#:*\r\n\t") {
			t.Fatalf("Normalize(%q) = %q", in, d)
		}
		for _, l := range strings.Split(d, ".") {
			if !labelRe.MatchString(l) {
				t.Fatalf("Normalize(%q) = %q has a bad label %q", in, d, l)
			}
		}
		if again, err := Normalize(d); err != nil || again != d {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", in, d, again, err)
		}
	})
}

// Whatever the hosts file looks like, cleaning it never leaves a managed line,
// is stable, and re-adding the block always verifies.
func FuzzHostsMerge(f *testing.F) {
	f.Add("127.0.0.1 localhost\r\n")
	f.Add(hostsBlock([]string{"youtube.com"}))
	f.Add(hostsBegin + "\r\nstray\r\n")
	f.Add("127.0.0.1 x" + hostsTag + "\n" + hostsEnd)
	f.Add("")
	domains := []string{"youtube.com", "a-b.org"}
	f.Fuzz(func(t *testing.T, content string) {
		clean := stripHosts(content)
		for _, ln := range strings.Split(clean, "\n") { // markers and tags are whole-line things
			if s := strings.TrimSpace(ln); s == hostsBegin || s == hostsEnd || strings.HasSuffix(s, hostsTag) {
				t.Fatalf("a managed line survived: %q", ln)
			}
		}
		if stripHosts(clean) != clean {
			t.Fatalf("strip is not stable for %q", content)
		}
		merged := mergeHosts(content, domains)
		if !hostsOK(merged, domains) {
			t.Fatalf("merged content does not verify: %q", merged)
		}
		if mergeHosts(merged, nil) != mergeHosts(clean, nil) {
			t.Fatalf("removing the block does not give the cleaned file back")
		}
	})
}

// A garbage datagram must never take the resolver down or stop it answering.
func TestSinkholeSurvivesGarbage(t *testing.T) {
	s := NewSinkhole()
	s.Set([]string{"youtube.com"}, time.Now().Add(time.Hour))
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := s.dnsServer("udp") // the same settings the real listener uses
	srv.PacketConn = pc
	started := make(chan struct{})
	srv.NotifyStartedFunc = func() { close(started) }
	go srv.ActivateAndServe()
	<-started
	defer srv.Shutdown()

	conn, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 500; i++ {
		b := make([]byte, rng.Intn(600))
		rng.Read(b)
		conn.Write(b)
	}
	big := make([]byte, 20000) // one oversized datagram used to end the server's read loop on Windows
	rng.Read(big)
	conn.Write(big)
	// well-formed header shapes that are still strange
	for _, q := range []func() *dns.Msg{
		func() *dns.Msg { return new(dns.Msg) }, // no question at all
		func() *dns.Msg {
			m := new(dns.Msg)
			m.SetQuestion("youtube.com.", dns.TypeA)
			m.Question = append(m.Question, m.Question[0])
			return m
		},
		func() *dns.Msg {
			m := new(dns.Msg)
			m.SetQuestion(strings.Repeat("a.", 120)+"com.", dns.TypeA)
			return m
		},
		func() *dns.Msg { m := new(dns.Msg); m.SetQuestion(".", dns.TypeANY); return m },
		func() *dns.Msg { m := new(dns.Msg); m.SetQuestion("youtube.com.", 65535); return m },
	} {
		if p, err := q().Pack(); err == nil {
			conn.Write(p)
		}
	}
	time.Sleep(100 * time.Millisecond)

	m := new(dns.Msg)
	m.SetQuestion("m.youtube.com.", dns.TypeA)
	r, _, err := new(dns.Client).Exchange(m, pc.LocalAddr().String())
	if err != nil || len(r.Answer) != 1 {
		t.Fatalf("resolver stopped answering after garbage: %v %v", r, err)
	}
}
