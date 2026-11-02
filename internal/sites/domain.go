// Package sites blocks websites at the OS level: hosts file, a local DNS
// sinkhole reached through NRPT rules, firewall rules against encrypted-DNS
// bypass, and browser policies that turn the bypasses off.
package sites

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Normalize turns user input ("https://www.YouTube.com/watch?v=1") into the
// bare registrable form ("youtube.com"). Subdomains and www are always covered.
func Normalize(in string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(in))
	if s == "" {
		return "", errors.New("empty domain")
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", fmt.Errorf("bad address %q", in)
		}
		s = u.Host
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.TrimPrefix(strings.TrimPrefix(strings.TrimSuffix(s, "."), "*."), "www.")
	if net.ParseIP(s) != nil {
		return "", fmt.Errorf("%q is an IP address; list a domain name instead", in)
	}
	if len(s) > 253 || !strings.Contains(s, ".") {
		return "", fmt.Errorf("%q is not a domain like example.com", in)
	}
	for _, l := range strings.Split(s, ".") {
		if !labelRe.MatchString(l) {
			return "", fmt.Errorf("%q is not a valid domain", in)
		}
	}
	return s, nil
}

// NormalizeAll normalizes, dedupes, and keeps order.
func NormalizeAll(in []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		d, err := Normalize(s)
		if err != nil {
			return nil, err
		}
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out, nil
}

// Hosts the app itself relies on (time sync, updates) are never blocked.
var protectedHosts = []string{"github.com", "githubusercontent.com", "api.github.com", "cloudflare.com", "www.google.com"}

// Protected reports whether blocking d would cut the app off from time sync or updates.
func Protected(d string) bool {
	for _, p := range protectedHosts {
		if p == d || strings.HasSuffix(p, "."+d) || strings.HasSuffix(d, "."+p) {
			return true
		}
	}
	return false
}

// match returns the listed domain that covers name (the domain itself or any subdomain).
func match(set map[string]bool, name string) (string, bool) {
	name = strings.ToLower(strings.TrimSuffix(name, "."))
	for name != "" {
		if set[name] {
			return name, true
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			break
		}
		name = name[i+1:]
	}
	return "", false
}
