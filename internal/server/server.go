// Package server is the local dashboard: a JSON API plus the embedded web UI,
// bound to 127.0.0.1 only.
//
// Defences against a hostile web page or local process:
//   - loopback listener, and a Host allow-list (stops DNS rebinding)
//   - every state-changing or sensitive call needs the per-run page token, which
//     only the served page contains, so another origin cannot guess it (CSRF)
//   - an Origin / Sec-Fetch-Site check on top of that
//   - sensitive calls additionally need a passcode session cookie
//     (HttpOnly, SameSite=Strict, 10 minute idle timeout)
//   - risky actions ask for the passcode again in the request itself
package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/auth"
	"github.com/Azaxek/focus-blocker/internal/engine"
	"github.com/Azaxek/focus-blocker/internal/store"
	"github.com/Azaxek/focus-blocker/internal/updater"
)

const sessionTTL = 10 * time.Minute

// Port is the loopback port of the dashboard. Only `focusguard run --port` (development) changes it.
var Port = 47820

//go:embed web
var webFS embed.FS

type Server struct {
	Version string
	Eng     *engine.Engine
	Auth    *auth.Auth
	Store   *store.Store
	Upd     *updater.Manager
	// Procs lists running process names (for the "add an app" helper).
	Procs func() []string
	// Stop shuts the service down gracefully (called after the passcode check).
	Stop func()

	token   string
	started time.Time

	mu        sync.Mutex
	sessions  map[string]time.Time
	uninstall struct {
		pending       bool
		approvedUntil time.Time
	}
}

func New(version string) *Server {
	b := make([]byte, 24)
	rand.Read(b)
	return &Server{Version: version, token: hex.EncodeToString(b), started: time.Now(), sessions: map[string]time.Time{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	sub, _ := fs.Sub(webFS, "web")
	static := http.FileServer(http.FS(sub))
	index, _ := fs.ReadFile(sub, "index.html")
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		// the first paint needs no round trip: status and config ride inside the page
		boot, _ := json.Marshal(map[string]any{"s": s.statusPayload(r), "c": s.configPayload()})
		boot = bytes.ReplaceAll(boot, []byte("</"), []byte(`<\/`))
		page := bytes.ReplaceAll(index, []byte("__FG_TOKEN__"), []byte(s.token))
		w.Write(bytes.ReplaceAll(page, []byte("__FG_BOOT__"), boot))
	})
	mux.Handle("GET /static/", http.StripPrefix("/static/", static))

	public := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, h) }
	guarded := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.needToken(h)) }
	secure := func(p string, h http.HandlerFunc) { mux.HandleFunc(p, s.needToken(s.needSession(h))) }

	public("GET /api/ping", s.ping)
	public("GET /api/status", s.status)
	public("GET /api/config", s.getConfig)
	public("GET /api/events", s.events)
	guarded("POST /api/auth/setup", s.setup)
	guarded("POST /api/auth/login", s.login)
	guarded("POST /api/auth/logout", s.logout)
	guarded("POST /api/uninstall/request", s.uninstallRequest)
	guarded("POST /api/uninstall/approve", s.uninstallApprove)
	guarded("GET /api/uninstall/status", s.uninstallStatus)
	guarded("POST /api/rewards/redeem", s.redeemReward) // the child spends earned time: no passcode by design
	guarded("POST /api/rewards/end", s.endBreak)        // ending a break only makes things stricter
	secure("POST /api/rewards/grant", s.grantReward)    // and the parent re-enters the passcode for every grant
	secure("POST /api/rewards/revoke", s.revokeReward)
	secure("PUT /api/config", s.putConfig)
	secure("POST /api/auth/change", s.changePass)
	secure("POST /api/windows/end", s.endWindow)
	secure("GET /api/log", s.getLog)
	secure("GET /api/updates", s.getUpdates)
	secure("POST /api/updates/check", s.checkUpdates)
	secure("POST /api/updates/install", s.installUpdate)
	secure("PUT /api/updates/settings", s.putUpdateSettings)
	secure("GET /api/processes", s.processes)
	secure("POST /api/service/stop", s.stopService)

	return s.baseline(mux)
}

// baseline applies headers and the Host check to everything.
func (s *Server) baseline(next http.Handler) http.Handler {
	ok := map[string]bool{"127.0.0.1:" + strconv.Itoa(Port): true, "localhost:" + strconv.Itoa(Port): true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ok[strings.ToLower(r.Host)] {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) needToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-FG-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			fail(w, http.StatusForbidden, "bad token", "token")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && o != "http://127.0.0.1:"+strconv.Itoa(Port) && o != "http://localhost:"+strconv.Itoa(Port) {
			fail(w, http.StatusForbidden, "bad origin", "origin")
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
			fail(w, http.StatusForbidden, "cross-site request", "origin")
			return
		}
		next(w, r)
	}
}

func (s *Server) newSession(w http.ResponseWriter) {
	b := make([]byte, 32)
	rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[id] = time.Now().Add(sessionTTL)
	for k, exp := range s.sessions {
		if time.Now().After(exp) {
			delete(s.sessions, k)
		}
	}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "fg_session", Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionTTL.Seconds())})
}

// hasSession reports whether the request carries a live passcode session and,
// if so, extends it: real use keeps a session open.
func (s *Server) hasSession(r *http.Request) bool { return s.session(r, true) }

// peekSession checks without extending. The dashboard polls the status every
// few seconds; if that counted as use, a page left open would never lock.
func (s *Server) peekSession(r *http.Request) bool { return s.session(r, false) }

func (s *Server) session(r *http.Request, slide bool) bool {
	c, err := r.Cookie("fg_session")
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.sessions[c.Value]
	if !ok || time.Now().After(exp) {
		delete(s.sessions, c.Value)
		return false
	}
	if slide {
		s.sessions[c.Value] = time.Now().Add(sessionTTL)
	}
	return true
}

func (s *Server) needSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.hasSession(r) {
			fail(w, http.StatusUnauthorized, "enter the parent passcode", "auth")
			return
		}
		next(w, r)
	}
}

// --- helpers ------------------------------------------------------------

func send(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg, kind string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg, "code": kind})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		fail(w, http.StatusBadRequest, "bad request", "request")
		return false
	}
	return true
}

func (s *Server) now() time.Time { return s.Eng.Clock.Now() }

// passFail turns an auth error into a response (and an activity-log line).
func (s *Server) passFail(w http.ResponseWriter, err error) {
	var wrong *auth.WrongError
	var locked *auth.LockedError
	switch {
	case errors.As(err, &wrong):
		s.Store.Log(s.now(), "auth", "Wrong passcode entered")
		fail(w, http.StatusForbidden, err.Error(), "wrong")
	case errors.As(err, &locked):
		s.Store.Log(s.now(), "auth", "Passcode locked until "+locked.Until.In(time.Local).Format("3:04 PM")+" after too many wrong attempts")
		fail(w, http.StatusTooManyRequests, err.Error(), "locked")
	case errors.Is(err, auth.ErrNotSet):
		fail(w, http.StatusConflict, "no passcode has been set yet", "setup")
	default:
		fail(w, http.StatusInternalServerError, err.Error(), "error")
	}
}

type passBody struct {
	Passcode string `json:"passcode"`
}

// reauth reads {"passcode": ...} out of a request and verifies it.
func (s *Server) reauth(w http.ResponseWriter, r *http.Request, extra any) bool {
	var b struct {
		passBody
	}
	raw, _ := io.ReadAll(r.Body)
	if json.Unmarshal(raw, &b) != nil {
		fail(w, http.StatusBadRequest, "bad request", "request")
		return false
	}
	if extra != nil {
		if json.Unmarshal(raw, extra) != nil {
			fail(w, http.StatusBadRequest, "bad request", "request")
			return false
		}
	}
	if err := s.Auth.Verify(b.Passcode); err != nil {
		s.passFail(w, err)
		return false
	}
	return true
}

// --- handlers -----------------------------------------------------------

func (s *Server) ping(w http.ResponseWriter, r *http.Request) {
	send(w, map[string]any{"ok": true, "app": "focusguard", "version": s.Version, "uptime": int(time.Since(s.started).Seconds())})
}

func (s *Server) statusPayload(r *http.Request) map[string]any {
	st := s.Eng.Status()
	s.mu.Lock()
	pending := s.uninstall.pending
	s.mu.Unlock()
	return map[string]any{
		"status":           st,
		"setup":            s.Auth.IsSet(),
		"lockedUntil":      nullTime(s.Auth.LockedUntil()),
		"unlocked":         s.peekSession(r),
		"uninstallPending": pending,
		"updateReady":      s.updateReady(),
	}
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) { send(w, s.statusPayload(r)) }

func (s *Server) configPayload() map[string]any {
	return map[string]any{"config": s.Store.Config(), "sitePresets": store.SitePresets, "categoryOrder": store.CategoryOrder, "appPresets": apps.Presets}
}

func (s *Server) updateReady() string {
	if s.Upd == nil {
		return ""
	}
	return s.Upd.Ready()
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) { send(w, s.configPayload()) }

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var c store.Config
	if !decode(w, r, &c) {
		return
	}
	old := s.Store.Config()
	c.AutoUpdate = old.AutoUpdate // update settings have their own endpoint
	if err := s.Store.SetConfig(c); err != nil {
		fail(w, http.StatusBadRequest, err.Error(), "invalid")
		return
	}
	s.Store.Log(s.now(), "config", "Schedule or block lists changed (passcode session)")
	s.Eng.Wake()
	send(w, map[string]any{"config": s.Store.Config()})
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	send(w, s.Eng.EventsSince(after))
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var b passBody
	if !decode(w, r, &b) {
		return
	}
	if err := s.Auth.Set(b.Passcode); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, auth.ErrAlreadySet) {
			code = http.StatusConflict
		}
		fail(w, code, err.Error(), "invalid")
		return
	}
	s.Store.Log(s.now(), "auth", "Parent passcode created")
	s.newSession(w)
	send(w, map[string]bool{"ok": true})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var b passBody
	if !decode(w, r, &b) {
		return
	}
	if err := s.Auth.Verify(b.Passcode); err != nil {
		s.passFail(w, err)
		return
	}
	s.Store.Log(s.now(), "auth", "Parent unlocked the dashboard")
	s.newSession(w)
	send(w, map[string]bool{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("fg_session"); err == nil {
		s.mu.Lock()
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: "fg_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	send(w, map[string]bool{"ok": true})
}

func (s *Server) changePass(w http.ResponseWriter, r *http.Request) {
	var b struct{ Old, New string }
	if !decode(w, r, &b) {
		return
	}
	if err := s.Auth.Change(b.Old, b.New); err != nil {
		if errors.Is(err, auth.ErrShort) || errors.Is(err, auth.ErrLong) {
			fail(w, http.StatusBadRequest, err.Error(), "invalid")
			return
		}
		s.passFail(w, err)
		return
	}
	s.Store.Log(s.now(), "auth", "Parent passcode changed")
	send(w, map[string]bool{"ok": true})
}

func (s *Server) endWindow(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Key string `json:"key"`
	}
	if !s.reauth(w, r, &b) {
		return
	}
	if err := s.Eng.EndEarly(b.Key); err != nil {
		fail(w, http.StatusBadRequest, err.Error(), "invalid")
		return
	}
	send(w, map[string]bool{"ok": true})
}

// --- free time (rewards) --------------------------------------------------

func (s *Server) grantReward(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Minutes int    `json:"minutes"`
		Reason  string `json:"reason"`
		Days    int    `json:"days"`
	}
	if !s.reauth(w, r, &b) {
		return
	}
	if b.Days == 0 {
		b.Days = 1
	}
	rw, err := s.Eng.GrantReward(b.Minutes, b.Reason, b.Days)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error(), "invalid")
		return
	}
	send(w, map[string]any{"reward": rw})
}

func (s *Server) revokeReward(w http.ResponseWriter, r *http.Request) {
	var b struct {
		ID string `json:"id"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := s.Eng.RevokeReward(b.ID); err != nil {
		fail(w, http.StatusBadRequest, err.Error(), "invalid")
		return
	}
	send(w, map[string]bool{"ok": true})
}

func (s *Server) redeemReward(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Minutes int `json:"minutes"` // 0 = as much as possible
	}
	if !decode(w, r, &b) {
		return
	}
	br, err := s.Eng.StartBreak(b.Minutes)
	if err != nil {
		fail(w, http.StatusConflict, err.Error(), "invalid")
		return
	}
	send(w, map[string]any{"until": br.Until})
}

func (s *Server) endBreak(w http.ResponseWriter, r *http.Request) {
	back, err := s.Eng.EndBreak()
	if err != nil {
		fail(w, http.StatusConflict, err.Error(), "invalid")
		return
	}
	send(w, map[string]int{"savedSeconds": back})
}

func (s *Server) getLog(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	var kinds []string
	if k := r.URL.Query().Get("kind"); k != "" {
		kinds = strings.Split(k, ",")
	}
	send(w, s.Store.ReadLog(limit, kinds...))
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request) {
	st := s.Store.State()
	send(w, map[string]any{
		"current": s.Version, "lastCheck": nullTime(st.LastCheck), "lastResult": st.LastCheckResult,
		"ready": s.updateReady(), "auto": s.Store.Config().AutoUpdate,
		"history": s.Store.ReadLog(50, "update"),
	})
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	if s.Upd == nil {
		fail(w, http.StatusNotImplemented, "updates are not available in this build", "invalid")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	msg, err := s.Upd.CheckNow(ctx)
	if err != nil {
		fail(w, http.StatusBadGateway, msg, "update")
		return
	}
	send(w, map[string]string{"result": msg})
}

// installUpdate downloads (if needed) and installs the newest release right away,
// but only when no focus window is active or about to start.
func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	if s.Upd == nil {
		fail(w, http.StatusNotImplemented, "updates are not available in this build", "invalid")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	msg, err := s.Upd.InstallNow(ctx)
	if err != nil {
		fail(w, http.StatusConflict, err.Error(), "update")
		return
	}
	send(w, map[string]string{"result": msg})
}

func (s *Server) putUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Auto bool `json:"auto"`
	}
	if !decode(w, r, &b) {
		return
	}
	c := s.Store.Config()
	c.AutoUpdate = b.Auto
	if err := s.Store.SetConfig(c); err != nil {
		fail(w, http.StatusInternalServerError, err.Error(), "error")
		return
	}
	s.Store.Log(s.now(), "config", "Automatic updates "+map[bool]string{true: "enabled", false: "disabled"}[b.Auto])
	send(w, map[string]bool{"auto": b.Auto})
}

func (s *Server) processes(w http.ResponseWriter, r *http.Request) {
	var names []string
	if s.Procs != nil {
		names = s.Procs()
	}
	sort.Strings(names)
	send(w, names)
}

func (s *Server) stopService(w http.ResponseWriter, r *http.Request) {
	if !s.reauth(w, r, nil) {
		return
	}
	s.Store.Log(s.now(), "service", "Service stopped with the parent passcode")
	send(w, map[string]bool{"ok": true})
	if s.Stop != nil {
		go func() { time.Sleep(300 * time.Millisecond); s.Stop() }()
	}
}

// --- uninstall authorisation (used by the installer) ----------------------

func (s *Server) uninstallRequest(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.uninstall.pending = true
	s.mu.Unlock()
	s.Store.Log(s.now(), "service", "Uninstall or reinstall requested; waiting for the parent passcode")
	send(w, map[string]bool{"ok": true})
}

func (s *Server) uninstallApprove(w http.ResponseWriter, r *http.Request) {
	if !s.reauth(w, r, nil) {
		return
	}
	s.mu.Lock()
	s.uninstall.pending = false
	s.uninstall.approvedUntil = time.Now().Add(5 * time.Minute)
	s.mu.Unlock()
	s.Store.Log(s.now(), "service", "Uninstall approved with the parent passcode")
	send(w, map[string]bool{"ok": true})
}

func (s *Server) uninstallStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ok := time.Now().Before(s.uninstall.approvedUntil)
	s.mu.Unlock()
	send(w, map[string]bool{"approved": ok})
}

// ListenAndServe serves on 127.0.0.1 until ctx ends.
func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(Port))
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
