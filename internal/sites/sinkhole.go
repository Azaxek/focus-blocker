package sites

import (
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// canary makes Firefox decide encrypted DNS is unwanted on this network.
const canary = "use-application-dns.net"

// Sinkhole answers DNS for blocked names with the loopback address and serves
// the "blocked until" page on port 80.
type Sinkhole struct {
	// OnAttempt is called (rate limited per domain) when something tries to reach a blocked site.
	OnAttempt func(domain string)

	mu      sync.RWMutex
	set     map[string]bool
	until   time.Time
	recent  map[string]time.Time
	servers []*dns.Server
	web     []*http.Server
	up      bool
}

func NewSinkhole() *Sinkhole {
	return &Sinkhole{set: map[string]bool{}, recent: map[string]time.Time{}}
}

// Set replaces the blocked domains and the time the block ends.
func (s *Sinkhole) Set(domains []string, until time.Time) {
	m := make(map[string]bool, len(domains))
	for _, d := range domains {
		m[d] = true
	}
	s.mu.Lock()
	s.set, s.until = m, until
	s.mu.Unlock()
}

func (s *Sinkhole) blocked(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return match(s.set, name)
}

// note counts an attempt at most once per domain every 5 seconds.
func (s *Sinkhole) note(domain string) {
	s.mu.Lock()
	last := s.recent[domain]
	ok := time.Since(last) > 5*time.Second
	if ok {
		s.recent[domain] = time.Now()
	}
	s.mu.Unlock()
	if ok && s.OnAttempt != nil {
		s.OnAttempt(domain)
	}
}

func (s *Sinkhole) dnsHandler() dns.Handler {
	return dns.HandlerFunc(func(w dns.ResponseWriter, r *dns.Msg) {
		defer func() { // whatever a client sends, the resolver for the whole PC must keep answering
			if recover() != nil {
				m := new(dns.Msg)
				m.SetRcode(r, dns.RcodeServerFailure)
				w.WriteMsg(m)
			}
		}()
		m := new(dns.Msg)
		m.SetReply(r)
		m.Authoritative = true
		if len(r.Question) == 1 {
			q := r.Question[0]
			name := q.Name
			if d, _ := match(map[string]bool{canary: true}, name); d != "" {
				m.Rcode = dns.RcodeNameError
			} else if d, ok := s.blocked(name); !ok {
				m.Rcode = dns.RcodeServerFailure // NRPT is stale; fail closed rather than forward
			} else {
				hdr := dns.RR_Header{Name: name, Rrtype: q.Qtype, Class: dns.ClassINET, Ttl: 1}
				switch q.Qtype {
				case dns.TypeA:
					hdr.Rrtype = dns.TypeA
					m.Answer = append(m.Answer, &dns.A{Hdr: hdr, A: net.IPv4(127, 0, 0, 1)})
					s.note(d)
				case dns.TypeAAAA:
					m.Answer = append(m.Answer, &dns.AAAA{Hdr: hdr, AAAA: net.IPv6loopback})
					s.note(d)
				} // HTTPS/SVCB and the rest: empty NOERROR, so clients fall back to A/AAAA
			}
		}
		w.WriteMsg(m)
	})
}

// dnsServer builds the listener for one network ("udp" or "tcp"). UDPSize is the
// largest possible datagram: on Windows a UDP packet bigger than the read buffer
// is a read error, and the DNS library treats that as fatal, so any local program
// could otherwise silence the sinkhole by sending one large packet to port 53.
func (s *Sinkhole) dnsServer(network string) *dns.Server {
	return &dns.Server{Addr: "127.0.0.1:53", Net: network, Handler: s.dnsHandler(), UDPSize: 65535}
}

func (s *Sinkhole) page() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil {
			host = r.Host
		}
		if d, ok := s.blocked(host); ok {
			s.note(d)
		}
		s.mu.RLock()
		until := s.until
		s.mu.RUnlock()
		when := "later"
		if !until.IsZero() {
			when = until.Format("3:04 PM")
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, blockPage, html.EscapeString(host), html.EscapeString(when))
	})
}

// Start binds DNS (UDP+TCP) on 127.0.0.1:53 and the block page on port 80.
// It starts whatever it can and reports what it could not.
func (s *Sinkhole) Start() error {
	s.Stop()
	var errs []error
	started := false
	for _, network := range []string{"udp", "tcp"} {
		srv := s.dnsServer(network)
		ready := make(chan struct{})
		srv.NotifyStartedFunc = func() { close(ready) }
		fail := make(chan error, 1)
		go func() {
			err := srv.ListenAndServe()
			fail <- err
			s.died(srv)
		}()
		select {
		case <-ready:
			s.mu.Lock()
			s.servers = append(s.servers, srv)
			s.mu.Unlock()
			started = true
		case err := <-fail:
			errs = append(errs, fmt.Errorf("dns %s: %w", network, err))
		case <-time.After(3 * time.Second):
			errs = append(errs, fmt.Errorf("dns %s: timed out", network))
		}
	}
	for _, addr := range []string{"127.0.0.1:80", "[::1]:80"} {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			errs = append(errs, fmt.Errorf("block page %s: %w", addr, err))
			continue
		}
		h := &http.Server{Handler: s.page(), ReadHeaderTimeout: 5 * time.Second}
		go h.Serve(ln)
		s.mu.Lock()
		s.web = append(s.web, h)
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.up = started
	s.mu.Unlock()
	return errors.Join(errs...)
}

// Running reports whether the DNS sinkhole is listening.
func (s *Sinkhole) Running() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.up
}

func (s *Sinkhole) Stop() {
	s.mu.Lock()
	servers, web := s.servers, s.web
	s.servers, s.web, s.up = nil, nil, false
	s.mu.Unlock()
	for _, srv := range servers {
		srv.Shutdown()
	}
	for _, h := range web {
		h.Close()
	}
}

// died notes that a DNS server stopped on its own rather than through Stop, so
// Running turns false and the watchdog starts the sinkhole again.
func (s *Sinkhole) died(srv *dns.Server) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range s.servers { // Stop empties the list first, so an orderly stop is not a death
		if x == srv {
			s.up = false
		}
	}
}

const blockPage = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Blocked</title>
<style>
:root{color-scheme:light dark}
body{margin:0;min-height:100vh;display:grid;place-items:center;font:16px/1.5 system-ui,Segoe UI,sans-serif;background:#0f1420;color:#e8ecf4}
main{max-width:30rem;padding:2rem;text-align:center}
.ring{width:5rem;height:5rem;border-radius:50%%;margin:0 auto 1.5rem;background:conic-gradient(#6c8cff,#a06cff,#6c8cff);display:grid;place-items:center}
.ring::after{content:"";width:3.4rem;height:3.4rem;border-radius:50%%;background:#0f1420}
h1{font-size:1.6rem;margin:.2rem 0}
p{color:#9aa6bf;margin:.4rem 0}
b{color:#e8ecf4}
</style>
<main><div class="ring"></div>
<h1>Stay with it</h1>
<p><b>%s</b> is blocked during your focus time.</p>
<p>It opens again at <b>%s</b>.</p></main></html>`
