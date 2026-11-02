package sites

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	hostsBegin = "# BEGIN FOCUSGUARD (managed block, do not edit)"
	hostsEnd   = "# END FOCUSGUARD"
)

// HostsPath is the Windows hosts file.
func HostsPath() string {
	return filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
}

// hostsBlock renders the managed block for domains ("" when there are none).
func hostsBlock(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(hostsBegin + "\r\n")
	for _, d := range domains {
		for _, h := range []string{d, "www." + d} {
			b.WriteString("127.0.0.1 " + h + "\r\n")
			b.WriteString("::1 " + h + "\r\n")
		}
	}
	b.WriteString(hostsEnd + "\r\n")
	return b.String()
}

// stripHosts removes any managed block, leaving every other line untouched.
func stripHosts(content string) string {
	var out []string
	skip := false
	for _, ln := range strings.SplitAfter(content, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case t == hostsBegin:
			skip = true
		case t == hostsEnd:
			skip = false
		case !skip:
			out = append(out, ln)
		}
	}
	return strings.Join(out, "")
}

// mergeHosts returns content with the managed block replaced by one for domains.
func mergeHosts(content string, domains []string) string {
	base := stripHosts(content)
	if base != "" && !strings.HasSuffix(base, "\n") {
		base += "\r\n"
	}
	return base + hostsBlock(domains)
}

// hostsOK reports whether content already carries exactly the block for domains.
func hostsOK(content string, domains []string) bool {
	return content == mergeHosts(content, domains)
}

func writeHosts(path string, domains []string) error {
	cur, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	want := mergeHosts(string(cur), domains)
	if want == string(cur) {
		return nil
	}
	os.Chmod(path, 0o666) // clears a read-only flag if someone set one
	return os.WriteFile(path, []byte(want), 0o644)
}

func hostsIntact(path string, domains []string) bool {
	cur, err := os.ReadFile(path)
	return err == nil && hostsOK(string(cur), domains)
}
