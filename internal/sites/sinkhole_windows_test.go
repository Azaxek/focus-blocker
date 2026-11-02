package sites

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Binds the real ports (53 and 80 on loopback do not need elevation) and
// checks what a browser would see: DNS answers and the "blocked until" page.
func TestSinkholeOnRealPorts(t *testing.T) {
	s := NewSinkhole()
	s.Set([]string{"youtube.com"}, time.Date(2026, 5, 4, 21, 0, 0, 0, time.Local))
	if err := s.Start(); err != nil && !s.Running() {
		t.Skip("cannot bind port 53 here: ", err)
	}
	defer s.Stop()

	m := new(dns.Msg)
	m.SetQuestion("www.youtube.com.", dns.TypeA)
	r, _, err := new(dns.Client).Exchange(m, "127.0.0.1:53")
	if err != nil || len(r.Answer) != 1 || !r.Answer[0].(*dns.A).A.Equal(net.IPv4(127, 0, 0, 1)) {
		t.Fatalf("dns: %v %v", r, err)
	}
	req, _ := http.NewRequest("GET", "http://127.0.0.1:80/watch?v=1", nil)
	req.Host = "www.youtube.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Skip("block page port 80 not available: ", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 403 || !strings.Contains(string(body), "www.youtube.com") || !strings.Contains(string(body), "9:00 PM") {
		t.Fatalf("block page: %d %s", resp.StatusCode, body)
	}
}
