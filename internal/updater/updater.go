// Package updater finds, verifies and installs new releases from one GitHub
// repository. Nothing is trusted from the network until the release's
// SHA256SUMS file verifies against the Ed25519 public key built into this
// binary and the installer matches the checksum inside it.
package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	maxInstaller = 300 << 20
	maxSmall     = 64 << 10
)

// Release is a newer, verified-on-download release.
type Release struct {
	Tag       string
	Version   string // without the leading v
	Installer string // asset name
	urls      map[string]string
}

type Updater struct {
	Repo    string            // "owner/name", fixed at build time
	PubKey  ed25519.PublicKey // release signing key, fixed at build time
	Current string            // running version, e.g. "1.2.0"
	Dir     string            // where downloads are staged

	// APIBase and Insecure exist for tests only.
	APIBase  string
	Insecure bool
	Client   *http.Client
}

var downloadHosts = map[string]bool{"github.com": true, "objects.githubusercontent.com": true, "release-assets.githubusercontent.com": true}

func (u *Updater) hostOK(raw string) error {
	p, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Insecure {
		return nil
	}
	if p.Scheme != "https" || !downloadHosts[p.Hostname()] {
		return fmt.Errorf("refusing %s: not an https GitHub address", p.Redacted())
	}
	return nil
}

func (u *Updater) client() *http.Client {
	if u.Client != nil {
		return u.Client
	}
	return &http.Client{
		Timeout: 15 * time.Minute,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 {
				return errors.New("too many redirects")
			}
			return u.hostOK(req.URL.String())
		},
	}
}

func (u *Updater) get(ctx context.Context, raw string, limit int64, accept string) ([]byte, error) {
	if err := u.hostOK(raw); err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	req.Header.Set("User-Agent", "FocusGuard-Updater")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := u.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", raw, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("response too large")
	}
	return b, err
}

var verRe = regexp.MustCompile(`^v?(\d{1,6})\.(\d{1,6})\.(\d{1,6})$`)

func parseVer(s string) ([3]int, bool) {
	m := verRe.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// Newer reports whether candidate is a strictly higher version than current.
// Anything unparseable (including a "dev" build) is never an upgrade target.
func Newer(candidate, current string) bool {
	c, ok1 := parseVer(candidate)
	cur, ok2 := parseVer(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if c[i] != cur[i] {
			return c[i] > cur[i]
		}
	}
	return false
}

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks GitHub for the latest release. It returns nil when there is
// nothing newer, or when the release is incomplete.
func (u *Updater) Check(ctx context.Context) (*Release, error) {
	if len(u.PubKey) != ed25519.PublicKeySize {
		return nil, errors.New("this build has no release signing key, so it cannot update itself")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`).MatchString(u.Repo) {
		return nil, errors.New("update repository is not configured")
	}
	base := u.APIBase
	if base == "" {
		base = "https://api.github.com"
	}
	b, err := u.get(ctx, base+"/repos/"+u.Repo+"/releases/latest", maxSmall*8, "application/vnd.github+json")
	if err != nil {
		return nil, err
	}
	var gr ghRelease
	if err := json.Unmarshal(b, &gr); err != nil {
		return nil, fmt.Errorf("unreadable release: %w", err)
	}
	if gr.Draft || gr.Prerelease || !Newer(gr.Tag, u.Current) {
		return nil, nil
	}
	ver := strings.TrimPrefix(gr.Tag, "v")
	rel := &Release{Tag: gr.Tag, Version: ver, Installer: "FocusGuard-Setup-" + ver + ".exe", urls: map[string]string{}}
	for _, a := range gr.Assets {
		if a.Name == rel.Installer || a.Name == "SHA256SUMS" || a.Name == "SHA256SUMS.sig" {
			rel.urls[a.Name] = a.URL
		}
	}
	for _, need := range []string{rel.Installer, "SHA256SUMS", "SHA256SUMS.sig"} {
		raw, ok := rel.urls[need]
		if !ok {
			return nil, fmt.Errorf("release %s is missing %s", gr.Tag, need)
		}
		if err := u.hostOK(raw); err != nil {
			return nil, err
		}
		if p, _ := url.Parse(raw); !u.Insecure && !strings.HasPrefix(p.Path, "/"+u.Repo+"/releases/download/") {
			return nil, fmt.Errorf("asset %s is not from %s", need, u.Repo)
		}
	}
	return rel, nil
}

// Download fetches and verifies the installer, returning its path.
func (u *Updater) Download(ctx context.Context, rel *Release) (string, error) {
	sums, err := u.get(ctx, rel.urls["SHA256SUMS"], maxSmall, "")
	if err != nil {
		return "", err
	}
	sigText, err := u.get(ctx, rel.urls["SHA256SUMS.sig"], 1024, "")
	if err != nil {
		return "", err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil || !ed25519.Verify(u.PubKey, sums, sig) {
		return "", errors.New("release signature is not valid: refusing to install")
	}
	want, err := checksumFor(sums, rel.Installer)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(u.Dir, 0o700); err != nil {
		return "", err
	}
	final := filepath.Join(u.Dir, rel.Installer)
	part := final + ".part"
	if err := u.fetchTo(ctx, rel.urls[rel.Installer], part, want); err != nil {
		os.Remove(part)
		return "", err
	}
	if err := os.Rename(part, final); err != nil {
		return "", err
	}
	return final, nil
}

func (u *Updater) fetchTo(ctx context.Context, raw, path, wantHex string) error {
	if err := u.hostOK(raw); err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	req.Header.Set("User-Agent", "FocusGuard-Updater")
	resp, err := u.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("installer download: HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxInstaller+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxInstaller {
		return errors.New("installer too large")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantHex {
		return fmt.Errorf("installer checksum mismatch (got %s…, expected %s…): refusing to install", got[:12], wantHex[:12])
	}
	return nil
}

// checksumFor reads `<sha256>  <name>` lines (sha256sum format).
func checksumFor(sums []byte, name string) (string, error) {
	for _, ln := range bytes.Split(sums, []byte("\n")) {
		f := strings.Fields(strings.TrimSpace(string(ln)))
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0]), nil
			}
		}
	}
	return "", fmt.Errorf("SHA256SUMS has no entry for %s", name)
}
