package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/store"
)

type fixture struct {
	srv       *httptest.Server
	u         *Updater
	installer []byte
	pub       ed25519.PublicKey
	priv      ed25519.PrivateKey
	// knobs
	tag, sumsOverride, sigOverride string
	prerelease                     bool
	hits                           map[string]int
}

func newFixture(t *testing.T) *fixture {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := &fixture{pub: pub, priv: priv, installer: []byte("MZ-fake-installer-bytes"), tag: "v1.3.0", hits: map[string]int{}}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	name := func() string { return "FocusGuard-Setup-" + strings.TrimPrefix(f.tag, "v") + ".exe" }
	sums := func() []byte {
		if f.sumsOverride != "" {
			return []byte(f.sumsOverride)
		}
		h := sha256.Sum256(f.installer)
		return []byte(hex.EncodeToString(h[:]) + "  " + name() + "\n")
	}
	mux.HandleFunc("/repos/Azaxek/focus-blocker/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		f.hits["api"]++
		base := "/Azaxek/focus-blocker/releases/download/" + f.tag + "/"
		json.NewEncoder(w).Encode(map[string]any{"tag_name": f.tag, "prerelease": f.prerelease, "assets": []map[string]string{
			{"name": name(), "browser_download_url": f.srv.URL + base + name()},
			{"name": "SHA256SUMS", "browser_download_url": f.srv.URL + base + "SHA256SUMS"},
			{"name": "SHA256SUMS.sig", "browser_download_url": f.srv.URL + base + "SHA256SUMS.sig"},
		}})
	})
	mux.HandleFunc("/Azaxek/focus-blocker/releases/download/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "SHA256SUMS.sig"):
			sig := ed25519.Sign(priv, sums())
			if f.sigOverride != "" {
				w.Write([]byte(f.sigOverride))
				return
			}
			w.Write([]byte(base64.StdEncoding.EncodeToString(sig)))
		case strings.HasSuffix(r.URL.Path, "SHA256SUMS"):
			w.Write(sums())
		default:
			f.hits["installer"]++
			w.Write(f.installer)
		}
	})
	f.u = &Updater{Repo: "Azaxek/focus-blocker", PubKey: pub, Current: "1.2.0", Dir: t.TempDir(), APIBase: f.srv.URL, Insecure: true}
	return f
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		cand, cur string
		want      bool
	}{
		{"v1.3.0", "1.2.0", true}, {"1.2.1", "1.2.0", true}, {"2.0.0", "1.9.9", true},
		{"v1.2.0", "1.2.0", false}, {"v1.1.9", "1.2.0", false}, {"v1.10.0", "1.9.0", true},
		{"v1.3.0", "dev", false}, {"nightly", "1.2.0", false}, {"v1.3.0-rc1", "1.2.0", false},
	} {
		if got := Newer(c.cand, c.cur); got != c.want {
			t.Errorf("Newer(%q,%q)=%v want %v", c.cand, c.cur, got, c.want)
		}
	}
}

func TestGoodUpdateIsDownloadedAndVerified(t *testing.T) {
	f := newFixture(t)
	rel, err := f.u.Check(context.Background())
	if err != nil || rel == nil || rel.Version != "1.3.0" {
		t.Fatalf("check: %v %v", rel, err)
	}
	p, err := f.u.Download(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != string(f.installer) {
		t.Fatal("wrong bytes staged")
	}
}

func TestNoUpdateWhenCurrentOrOlderOrPrerelease(t *testing.T) {
	f := newFixture(t)
	f.u.Current = "1.3.0"
	if rel, err := f.u.Check(context.Background()); rel != nil || err != nil {
		t.Fatalf("same version offered: %v %v", rel, err)
	}
	f.u.Current, f.tag = "1.2.0", "v1.1.0"
	if rel, _ := f.u.Check(context.Background()); rel != nil {
		t.Fatal("a downgrade was offered")
	}
	f.tag, f.prerelease = "v1.3.0", true
	if rel, _ := f.u.Check(context.Background()); rel != nil {
		t.Fatal("prerelease offered")
	}
}

func TestBadChecksumIsRejected(t *testing.T) {
	f := newFixture(t)
	f.sumsOverride = strings.Repeat("0", 64) + "  FocusGuard-Setup-1.3.0.exe\n" // validly signed, but wrong hash
	rel, _ := f.u.Check(context.Background())
	_, err := f.u.Download(context.Background(), rel)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(f.u.Dir, "*")); len(left) != 0 {
		t.Fatalf("a rejected installer was left on disk: %v", left)
	}
}

func TestBadSignatureIsRejected(t *testing.T) {
	f := newFixture(t)
	f.sigOverride = base64.StdEncoding.EncodeToString(make([]byte, 64))
	rel, _ := f.u.Check(context.Background())
	if _, err := f.u.Download(context.Background(), rel); err == nil || !strings.Contains(err.Error(), "signature") {
		t.Fatalf("want signature error, got %v", err)
	}
	if f.hits["installer"] != 0 {
		t.Fatal("installer was downloaded before the signature verified")
	}
}

func TestWrongSigningKeyIsRejected(t *testing.T) {
	f := newFixture(t)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	f.u.PubKey = other // build trusts a different key than the one that signed the release
	rel, _ := f.u.Check(context.Background())
	if _, err := f.u.Download(context.Background(), rel); err == nil {
		t.Fatal("release signed by an unknown key was accepted")
	}
}

func TestBuildWithoutKeyCannotUpdate(t *testing.T) {
	f := newFixture(t)
	f.u.PubKey = nil
	if _, err := f.u.Check(context.Background()); err == nil {
		t.Fatal("must fail closed without a key")
	}
}

func TestOnlyConfiguredRepoOverHTTPS(t *testing.T) {
	u := &Updater{Repo: "Azaxek/focus-blocker"}
	for _, bad := range []string{"http://github.com/x", "https://evil.example/x", "https://github.com.evil.io/x", "ftp://github.com/x"} {
		if u.hostOK(bad) == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
	if err := u.hostOK("https://objects.githubusercontent.com/abc"); err != nil {
		t.Error(err)
	}
	// assets from a different repository on github.com are refused too
	f := newFixture(t)
	f.u.Insecure = false
	f.u.Client = f.srv.Client()
	if _, err := f.u.Check(context.Background()); err == nil {
		t.Fatal("http test server must be refused when Insecure is off")
	}
}

// --- applying -----------------------------------------------------------

type fakeSteps struct {
	t            *testing.T
	installDir   string
	installerErr error
	brokenNew    bool // installs files but the new service never becomes healthy
	starts       int
	stops        int
	health       []string
}

func (s *fakeSteps) StopService() error  { s.stops++; return nil }
func (s *fakeSteps) StartService() error { s.starts++; return nil }
func (s *fakeSteps) RunInstaller(string) error {
	if s.installerErr != nil {
		os.WriteFile(filepath.Join(s.installDir, "focusguard.exe"), []byte("half-written"), 0o755)
		return s.installerErr
	}
	return os.WriteFile(filepath.Join(s.installDir, "focusguard.exe"), []byte("v1.3.0 binary"), 0o755)
}
func (s *fakeSteps) Healthy(v string) bool {
	s.health = append(s.health, v)
	return !(s.brokenNew && v == "1.3.0")
}

type dirs struct{ install, backup, data string }

func setupDirs(t *testing.T) dirs {
	d := dirs{t.TempDir(), filepath.Join(t.TempDir(), "backup"), t.TempDir()}
	os.WriteFile(filepath.Join(d.install, "focusguard.exe"), []byte("v1.2.0 binary"), 0o755)
	os.MkdirAll(filepath.Join(d.install, "sub"), 0o755)
	os.WriteFile(filepath.Join(d.install, "sub", "extra.dat"), []byte("x"), 0o644)
	return d
}

func params(d dirs) Params {
	return Params{Installer: "setup.exe", InstallDir: d.install, BackupDir: d.backup, From: "1.2.0", To: "1.3.0"}
}

func TestUpdateSuccess(t *testing.T) {
	d := setupDirs(t)
	s := &fakeSteps{t: t, installDir: d.install}
	r := RunApply(s, params(d))
	if !r.OK || r.RolledBack {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(d.install, "focusguard.exe")); string(b) != "v1.3.0 binary" {
		t.Fatal("new version not installed")
	}
}

func TestFailedInstallerRollsBack(t *testing.T) {
	d := setupDirs(t)
	s := &fakeSteps{t: t, installDir: d.install, installerErr: errors.New("exit 5")}
	r := RunApply(s, params(d))
	if r.OK || !r.RolledBack {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(d.install, "focusguard.exe")); string(b) != "v1.2.0 binary" {
		t.Fatalf("old binary not restored: %q", b)
	}
	if s.starts < 1 {
		t.Fatal("old version was not restarted")
	}
}

func TestUnhealthyNewVersionRollsBack(t *testing.T) {
	d := setupDirs(t)
	s := &fakeSteps{t: t, installDir: d.install, brokenNew: true}
	r := RunApply(s, params(d))
	if r.OK || !r.RolledBack {
		t.Fatalf("%+v", r)
	}
	if b, _ := os.ReadFile(filepath.Join(d.install, "focusguard.exe")); string(b) != "v1.2.0 binary" {
		t.Fatal("old binary not restored")
	}
	if s.starts != 2 || len(s.health) != 2 || s.health[1] != "1.2.0" {
		t.Fatalf("expected start, rollback start and a health check of the old version: starts=%d health=%v", s.starts, s.health)
	}
}

// The passcode hash, schedules, lists and logs live in the data directory.
// Neither a successful update nor a rolled-back one may alter any of it.
func TestSettingsAndPasscodeSurviveUpdateAndRollback(t *testing.T) {
	for name, steps := range map[string]func(dirs) *fakeSteps{
		"success":  func(d dirs) *fakeSteps { return &fakeSteps{t: t, installDir: d.install} },
		"rollback": func(d dirs) *fakeSteps { return &fakeSteps{t: t, installDir: d.install, brokenNew: true} },
	} {
		t.Run(name, func(t *testing.T) {
			d := setupDirs(t)
			st, _ := store.Open(d.data)
			c := st.Config()
			c.Sites = append(c.Sites, "example.com")
			st.SetConfig(c)
			os.WriteFile(filepath.Join(d.data, "auth.json"), []byte(`{"hash":"$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA","failures":3}`), 0o600)
			st.Log(time.Now(), "test", "kept")
			before := snapshot(t, d.data)

			RunApply(steps(d), params(d))

			if after := snapshot(t, d.data); after != before {
				t.Fatalf("data directory changed during update:\nbefore: %s\nafter:  %s", before, after)
			}
			st2, err := store.Open(d.data)
			if err != nil || st2.Config().Sites[len(st2.Config().Sites)-1] != "example.com" {
				t.Fatal("settings did not survive")
			}
		})
	}
}

func snapshot(t *testing.T, dir string) string {
	var out []string
	filepath.WalkDir(dir, func(p string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			b, _ := os.ReadFile(p)
			h := sha256.Sum256(b)
			out = append(out, fmt.Sprintf("%s=%x", e.Name(), h[:6]))
		}
		return nil
	})
	return strings.Join(out, ";")
}

func TestInstallNowWaitsForFreeTimeThenInstalls(t *testing.T) {
	f := newFixture(t)
	idle, applied := false, 0
	m := &Manager{U: f.u, Auto: func() bool { return true }, Idle: func() bool { return idle },
		Apply: func(string, *Release) error { applied++; return nil }, Log: func(string, string) {}, Record: func(time.Time, string) {}}

	if _, err := m.InstallNow(context.Background()); err == nil || !strings.Contains(err.Error(), "focus window") || applied != 0 {
		t.Fatalf("must not install during a window: err=%v applied=%d", err, applied)
	}
	if m.Ready() != "1.3.0" {
		t.Fatal("the update should be downloaded and waiting")
	}
	idle = true
	msg, err := m.InstallNow(context.Background())
	if err != nil || applied != 1 || !strings.Contains(msg, "1.3.0") {
		t.Fatalf("msg=%q err=%v applied=%d", msg, err, applied)
	}
	if msg, _ = m.InstallNow(context.Background()); applied != 1 || !strings.Contains(msg, "already installing") {
		t.Fatalf("a second click must not start a second install: %q applied=%d", msg, applied)
	}
}

func TestInstallNowWhenUpToDate(t *testing.T) {
	f := newFixture(t)
	f.u.Current = "1.3.0"
	m := &Manager{U: f.u, Idle: func() bool { return true }, Apply: func(string, *Release) error { t.Fatal("nothing to install"); return nil },
		Log: func(string, string) {}, Record: func(time.Time, string) {}}
	if msg, err := m.InstallNow(context.Background()); err != nil || !strings.Contains(msg, "up to date") {
		t.Fatalf("%q %v", msg, err)
	}
}

func TestResultFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if ConsumeResult(dir) != nil {
		t.Fatal("no result expected")
	}
	SaveResult(dir, Result{From: "1", To: "2", RolledBack: true, Message: "m"})
	r := ConsumeResult(dir)
	if r == nil || !r.RolledBack || ConsumeResult(dir) != nil {
		t.Fatalf("%+v", r)
	}
}
