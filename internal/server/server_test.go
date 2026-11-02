package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/auth"
	"github.com/Azaxek/focus-blocker/internal/clock"
	"github.com/Azaxek/focus-blocker/internal/engine"
	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/store"
)

type nopSites struct{}

func (nopSites) Reconcile([]string, time.Time) ([]string, error) { return nil, nil }

type nopApps struct{}

func (nopApps) SetRules([]apps.Rule) {}
func (nopApps) Scan() []apps.Killed  { return nil }

func init() { auth.Params.Memory, auth.Params.Time = 8, 1 }

type rig struct {
	t     *testing.T
	s     *Server
	h     http.Handler
	token string
	jar   *cookiejar.Jar
}

func newRig(t *testing.T) *rig {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.New()
	s := New("1.0.0")
	s.Store, s.Eng = st, engine.New("1.0.0", st, clk, nopSites{}, nopApps{})
	s.Auth = auth.New(filepath.Join(t.TempDir(), "auth.json"), clk.Now)
	s.Stop = func() {}
	r := &rig{t: t, s: s, h: s.Handler(), token: s.token}
	r.jar, _ = cookiejar.New(nil)
	return r
}

// do sends a request as the local dashboard page would.
func (r *rig) do(method, path string, body any, mods ...func(*http.Request)) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, "http://127.0.0.1:47820"+path, &buf)
	req.Host = "127.0.0.1:47820"
	req.Header.Set("X-FG-Token", r.token)
	req.Header.Set("Origin", "http://127.0.0.1:47820")
	u, _ := url.Parse("http://127.0.0.1:47820/")
	for _, c := range r.jar.Cookies(u) {
		req.AddCookie(c)
	}
	for _, m := range mods {
		m(req)
	}
	w := httptest.NewRecorder()
	r.h.ServeHTTP(w, req)
	r.jar.SetCookies(u, w.Result().Cookies())
	return w
}

func code(w *httptest.ResponseRecorder) int { return w.Code }

func (r *rig) setup() {
	if w := r.do("POST", "/api/auth/setup", map[string]string{"passcode": "parent-secret"}); w.Code != 200 {
		r.t.Fatalf("setup: %d %s", w.Code, w.Body)
	}
}

func TestIndexCarriesFreshTokenAndSecurityHeaders(t *testing.T) {
	r := newRig(t)
	w := r.do("GET", "/", nil)
	m := regexp.MustCompile(`content="([0-9a-f]{48})"`).FindStringSubmatch(w.Body.String())
	if m == nil || m[1] != r.token {
		t.Fatalf("token not injected: %s", w.Body)
	}
	if New("x").token == r.token {
		t.Fatal("tokens must be random per run")
	}
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if w.Header().Get(h) == "" {
			t.Errorf("missing %s", h)
		}
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("CSP must forbid framing")
	}
}

// The first paint must not wait for any request: status and config ride inside the page.
func TestIndexInlinesFirstPaintData(t *testing.T) {
	r := newRig(t)
	body := r.do("GET", "/", nil).Body.String()
	m := regexp.MustCompile(`(?s)<script id="boot" type="application/json">(.*?)</script>`).FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no boot data in the page")
	}
	var boot struct {
		S struct {
			Status struct{ Version string }
			Setup  bool
		}
		C struct{ Config struct{ Sites []string } }
	}
	if err := json.Unmarshal([]byte(m[1]), &boot); err != nil {
		t.Fatalf("boot data is not valid JSON: %v", err)
	}
	if boot.S.Status.Version != "1.0.0" || len(boot.C.Config.Sites) == 0 || boot.S.Setup {
		t.Fatalf("unexpected boot data: %+v", boot)
	}
	if strings.Contains(m[1], "</") {
		t.Fatal("boot JSON could close its own script tag")
	}
	if strings.Contains(body, "__FG_BOOT__") || strings.Contains(body, "__FG_TOKEN__") {
		t.Fatal("placeholders were not replaced")
	}
}

func TestHostHeaderAllowList(t *testing.T) {
	r := newRig(t)
	for host, want := range map[string]int{"127.0.0.1:47820": 200, "localhost:47820": 200, "evil.com:47820": 403, "127.0.0.1.evil.com:47820": 403, "127.0.0.1:80": 403} {
		w := r.do("GET", "/api/ping", nil, func(q *http.Request) { q.Host = host })
		if w.Code != want {
			t.Errorf("Host %s: got %d want %d", host, w.Code, want)
		}
	}
}

func TestStateChangesNeedTheToken(t *testing.T) {
	r := newRig(t)
	no := func(q *http.Request) { q.Header.Del("X-FG-Token") }
	bad := func(q *http.Request) { q.Header.Set("X-FG-Token", "guess") }
	for _, m := range []func(*http.Request){no, bad} {
		for _, p := range []string{"/api/auth/setup", "/api/auth/login", "/api/uninstall/request"} {
			if w := r.do("POST", p, map[string]string{"passcode": "x"}, m); w.Code != 403 {
				t.Errorf("%s without a valid token: %d", p, w.Code)
			}
		}
	}
}

func TestCrossOriginRequestsRefused(t *testing.T) {
	r := newRig(t)
	r.setup()
	cases := []func(*http.Request){
		func(q *http.Request) { q.Header.Set("Origin", "https://evil.example") },
		func(q *http.Request) { q.Header.Set("Origin", "null") },
		func(q *http.Request) { q.Header.Set("Sec-Fetch-Site", "cross-site") },
	}
	for i, m := range cases {
		if w := r.do("POST", "/api/auth/login", map[string]string{"passcode": "parent-secret"}, m); w.Code != 403 {
			t.Errorf("case %d: %d", i, w.Code)
		}
	}
}

func TestFirstRunSetupOnlyOnce(t *testing.T) {
	r := newRig(t)
	if w := r.do("POST", "/api/auth/setup", map[string]string{"passcode": "short"}); w.Code != 400 {
		t.Fatalf("short passcode: %d", w.Code)
	}
	r.setup()
	if w := r.do("POST", "/api/auth/setup", map[string]string{"passcode": "another-one"}); w.Code != 409 {
		t.Fatalf("second setup must be refused: %d", w.Code)
	}
}

func TestEditsNeedAPasscodeSession(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.do("POST", "/api/auth/logout", nil)
	cfg := r.s.Store.Config()
	for _, c := range []struct{ m, p string }{{"PUT", "/api/config"}, {"GET", "/api/log"}, {"GET", "/api/updates"}, {"POST", "/api/windows/end"}, {"POST", "/api/service/stop"}, {"POST", "/api/auth/change"}, {"PUT", "/api/updates/settings"}} {
		if w := r.do(c.m, c.p, cfg); w.Code != 401 {
			t.Errorf("%s %s without a session: %d", c.m, c.p, w.Code)
		}
	}
	if w := r.do("POST", "/api/auth/login", map[string]string{"passcode": "parent-secret"}); w.Code != 200 {
		t.Fatal(w.Body)
	}
	u, _ := url.Parse("http://127.0.0.1:47820/")
	ck := r.jar.Cookies(u)
	if len(ck) != 1 {
		t.Fatal("no session cookie")
	}
	cfg.Windows = []schedule.Window{{ID: "w", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"}}
	if w := r.do("PUT", "/api/config", cfg); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if len(r.s.Store.Config().Windows) != 1 {
		t.Fatal("edit not saved")
	}
	if w := r.do("GET", "/api/log", nil); w.Code != 200 {
		t.Fatal("log should be readable with a session")
	}
}

func TestSessionCookieFlags(t *testing.T) {
	r := newRig(t)
	w := r.do("POST", "/api/auth/setup", map[string]string{"passcode": "parent-secret"})
	c := w.Result().Cookies()
	if len(c) != 1 || !c[0].HttpOnly || c[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie: %+v", c)
	}
}

func TestLockoutOverHTTP(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.do("POST", "/api/auth/logout", nil)
	var last *httptest.ResponseRecorder
	for i := 0; i < 5; i++ {
		last = r.do("POST", "/api/auth/login", map[string]string{"passcode": "wrong-guess"})
	}
	if last.Code != 429 {
		t.Fatalf("5th wrong attempt should lock: %d %s", last.Code, last.Body)
	}
	if w := r.do("POST", "/api/auth/login", map[string]string{"passcode": "parent-secret"}); w.Code != 429 {
		t.Fatalf("correct passcode must not work while locked: %d", w.Code)
	}
	if strings.Contains(r.s.Store.ReadLog(1)[0].Msg, "wrong-guess") {
		t.Fatal("passcode leaked into the log")
	}
}

func TestEndingEarlyAsksForPasscodeAgain(t *testing.T) {
	r := newRig(t)
	r.setup() // session exists
	if w := r.do("POST", "/api/windows/end", map[string]string{"key": "x", "passcode": "wrong-guess"}); w.Code != 403 {
		t.Fatalf("a live session alone must not be enough: %d", w.Code)
	}
	if w := r.do("POST", "/api/windows/end", map[string]string{"key": "nope", "passcode": "parent-secret"}); w.Code != 400 {
		t.Fatalf("right passcode, unknown window: %d %s", w.Code, w.Body)
	}
}

func TestPublicEndpointsExposeNoSecrets(t *testing.T) {
	r := newRig(t)
	r.setup()
	r.do("POST", "/api/auth/logout", nil)
	for _, p := range []string{"/api/status", "/api/ping", "/api/config", "/api/events"} {
		w := r.do("GET", p, nil, func(q *http.Request) { q.Header.Del("X-FG-Token") })
		if w.Code != 200 {
			t.Errorf("%s: %d", p, w.Code)
		}
		if strings.Contains(w.Body.String(), "argon2") || strings.Contains(w.Body.String(), "parent-secret") {
			t.Errorf("%s leaks secrets", p)
		}
	}
}

// alwaysOn is a pair of back-to-back windows that cover every moment of every day.
func alwaysOn() []schedule.Window {
	all := []int{0, 1, 2, 3, 4, 5, 6}
	return []schedule.Window{
		{ID: "am", Name: "Morning", Days: all, Start: "00:00", End: "12:00"},
		{ID: "pm", Name: "Afternoon", Days: all, Start: "12:00", End: "00:00"},
	}
}

type statusBody struct {
	Status struct {
		Blocking bool
		Rewards  struct {
			BalanceSec   int
			CanPause     bool
			GrantedToday int
			Break        *struct{}
		}
	}
}

func (r *rig) status() statusBody {
	var s statusBody
	json.Unmarshal(r.do("GET", "/api/status", nil).Body.Bytes(), &s)
	return s
}

func TestFreeTimeFlowAndWhoMayDoWhat(t *testing.T) {
	r := newRig(t)
	r.setup()
	cfg := r.s.Store.Config()
	cfg.Windows = alwaysOn()
	if w := r.do("PUT", "/api/config", cfg); w.Code != 200 {
		t.Fatal(w.Body)
	}
	grant := func(pass string) int {
		return r.do("POST", "/api/rewards/grant", map[string]any{"minutes": 30, "reason": "Gym", "days": 2, "passcode": pass}).Code
	}
	if c := grant("not-the-passcode"); c != 403 {
		t.Fatalf("a live session alone must not be enough to grant: %d", c)
	}
	if c := grant("parent-secret"); c != 200 {
		t.Fatalf("grant: %d", c)
	}
	if s := r.status(); !s.Status.Blocking || s.Status.Rewards.BalanceSec != 1800 || !s.Status.Rewards.CanPause || s.Status.Rewards.GrantedToday != 30 {
		t.Fatalf("%+v", s)
	}

	// the child (no passcode session) spends it, and can hand it back
	r.do("POST", "/api/auth/logout", nil)
	if c := grant("parent-secret"); c != 401 {
		t.Fatalf("granting needs the unlocked dashboard: %d", c)
	}
	if w := r.do("POST", "/api/rewards/redeem", map[string]int{"minutes": 0}); w.Code != 200 {
		t.Fatalf("redeem: %d %s", w.Code, w.Body)
	}
	if s := r.status(); s.Status.Blocking || s.Status.Rewards.Break == nil {
		t.Fatalf("break should lift the block: %+v", s)
	}
	if w := r.do("POST", "/api/rewards/redeem", map[string]int{"minutes": 5}); w.Code != 409 {
		t.Fatalf("second break: %d", w.Code)
	}
	if w := r.do("POST", "/api/rewards/end", nil); w.Code != 200 {
		t.Fatalf("end: %d %s", w.Code, w.Body)
	}
	if s := r.status(); !s.Status.Blocking || s.Status.Rewards.BalanceSec < 1700 {
		t.Fatalf("blocking back, minutes returned: %+v", s)
	}
	if w := r.do("POST", "/api/rewards/end", nil); w.Code != 409 {
		t.Fatalf("nothing to end: %d", w.Code)
	}

	// token and origin rules apply to the child's calls too
	bad := func(q *http.Request) { q.Header.Set("Origin", "https://evil.example") }
	if w := r.do("POST", "/api/rewards/redeem", map[string]int{"minutes": 5}, bad); w.Code != 403 {
		t.Fatalf("cross-origin redeem: %d", w.Code)
	}
	// only a parent session may revoke
	if w := r.do("POST", "/api/rewards/revoke", map[string]string{"id": "x"}); w.Code != 401 {
		t.Fatalf("revoke without a session: %d", w.Code)
	}
}

func TestGrantInputIsChecked(t *testing.T) {
	r := newRig(t)
	r.setup()
	for _, m := range []int{0, 4, 500} {
		if w := r.do("POST", "/api/rewards/grant", map[string]any{"minutes": m, "passcode": "parent-secret"}); w.Code != 400 {
			t.Errorf("minutes=%d: %d", m, w.Code)
		}
	}
	r.do("POST", "/api/rewards/grant", map[string]any{"minutes": 60, "passcode": "parent-secret"})
	if w := r.do("POST", "/api/rewards/grant", map[string]any{"minutes": 5, "passcode": "parent-secret"}); w.Code != 400 || !strings.Contains(w.Body.String(), "limit") {
		t.Errorf("daily limit: %d %s", w.Code, w.Body)
	}
}

// The dashboard polls /api/status every few seconds. That must not keep a
// parent's session alive, or an unattended page would never lock.
func TestStatusPollingDoesNotExtendTheSession(t *testing.T) {
	r := newRig(t)
	r.setup()
	near := time.Now().Add(30 * time.Second)
	r.s.mu.Lock()
	for k := range r.s.sessions {
		r.s.sessions[k] = near
	}
	r.s.mu.Unlock()
	for i := 0; i < 3; i++ {
		r.do("GET", "/api/status", nil)
	}
	r.s.mu.Lock()
	for _, exp := range r.s.sessions {
		if !exp.Equal(near) {
			t.Fatalf("polling extended the session by %v", exp.Sub(near))
		}
	}
	r.s.mu.Unlock()
	if w := r.do("GET", "/api/log", nil); w.Code != 200 { // real use does extend it
		t.Fatal(w.Code)
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	for _, exp := range r.s.sessions {
		if exp.Sub(near) < time.Minute {
			t.Fatal("real use should keep the session alive")
		}
	}
}

func TestUninstallNeedsApproval(t *testing.T) {
	r := newRig(t)
	r.setup()
	check := func() bool {
		w := r.do("GET", "/api/uninstall/status", nil)
		var o map[string]bool
		json.Unmarshal(w.Body.Bytes(), &o)
		return o["approved"]
	}
	r.do("POST", "/api/uninstall/request", nil)
	if check() {
		t.Fatal("approved without a passcode")
	}
	if w := r.do("POST", "/api/uninstall/approve", map[string]string{"passcode": "nope-nope"}); w.Code != 403 || check() {
		t.Fatal("wrong passcode approved the uninstall")
	}
	if w := r.do("POST", "/api/uninstall/approve", map[string]string{"passcode": "parent-secret"}); w.Code != 200 || !check() {
		t.Fatal("right passcode should approve")
	}
}
