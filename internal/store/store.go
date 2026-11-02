// Package store persists the configuration, daily statistics and activity log
// in the (admin-only) data directory.
package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Azaxek/focus-blocker/internal/apps"
	"github.com/Azaxek/focus-blocker/internal/fsx"
	"github.com/Azaxek/focus-blocker/internal/rewards"
	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/sites"
)

const (
	schemaVersion = 1
	maxLogBytes   = 4 << 20
)

type Config struct {
	Version    int               `json:"version"`
	Timezone   string            `json:"timezone,omitempty"` // IANA name; empty = the machine's zone
	Windows    []schedule.Window `json:"windows"`
	Sites      []string          `json:"sites"`    // global blocked domains
	Apps       []string          `json:"apps"`     // global blocked app rule ids
	AppRules   []apps.Rule       `json:"appRules"` // every known rule: presets + custom
	AutoUpdate bool              `json:"autoUpdate"`
	// RewardCapMinutes is the most free time a parent can grant in one day (0 = the default, 60).
	RewardCapMinutes int `json:"rewardCapMinutes,omitempty"`
}

const (
	DefaultRewardCap = 60
	MaxRewardCap     = 600
)

// RewardCap is the daily limit on granted free time, in minutes.
func (c Config) RewardCap() int {
	if c.RewardCapMinutes <= 0 {
		return DefaultRewardCap
	}
	return c.RewardCapMinutes
}

type DayStat struct {
	FocusSec   int `json:"focusSec"`
	SiteBlocks int `json:"siteBlocks"`
	AppBlocks  int `json:"appBlocks"`
	EndedEarly int `json:"endedEarly"`
	RewardMin  int `json:"rewardMin,omitempty"` // free time granted that day
}

type State struct {
	Ended           map[string]time.Time `json:"ended"` // occurrence key -> its end
	Days            map[string]*DayStat  `json:"days"`
	LastCheck       time.Time            `json:"lastCheck"`
	LastCheckResult string               `json:"lastCheckResult"`
	Rewards         rewards.Ledger       `json:"rewards"`
}

type Entry struct {
	T    time.Time `json:"t"`
	Kind string    `json:"kind"`
	Msg  string    `json:"msg"`
}

type Store struct {
	dir string
	mu  sync.Mutex
	cfg Config
	st  State
	lg  sync.Mutex

	// Notice is set when a damaged file had to be restored or replaced; the
	// dashboard shows it so a parent notices.
	Notice string
}

func Default() Config {
	c := Config{Version: schemaVersion, Windows: []schedule.Window{}, AppRules: append([]apps.Rule{}, apps.Presets...),
		Apps: append([]string{}, apps.DefaultGlobal...), AutoUpdate: true}
	for _, cat := range DefaultCategories {
		c.Sites = append(c.Sites, SitePresets[cat]...)
	}
	return c
}

// Open loads (or creates) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	var problems []string
	cfg, how, err := load[Config](filepath.Join(dir, "config.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg = Default()
	case err != nil:
		// A service that refuses to start blocks nothing, so start from the defaults and say so loudly.
		cfg = Default()
		problems = append(problems, "The settings file was damaged and so was its backup, so Focus Guard started from the defaults (the damaged file was kept as config.json.damaged). Re-create the schedule.")
	case how != "":
		problems = append(problems, "The settings file was damaged or missing; the last good copy was restored (a damaged file is kept as config.json.damaged).")
	}
	s.cfg = cfg
	if err := s.saveConfig(); err != nil { // also (re)writes the .bak copy
		return nil, err
	}
	st, how, err := load[State](filepath.Join(dir, "state.json"))
	switch {
	case err != nil && !errors.Is(err, os.ErrNotExist):
		problems = append(problems, "The statistics file was damaged and was reset (kept as state.json.damaged).")
	case how != "":
		problems = append(problems, "The statistics file was damaged; the last good copy was restored.")
	}
	s.st = st
	if len(problems) > 0 {
		s.Notice = strings.Join(problems, " ")
		for _, p := range problems {
			s.Log(time.Now(), "error", p)
		}
	}
	if s.st.Ended == nil {
		s.st.Ended = map[string]time.Time{}
	}
	if s.st.Days == nil {
		s.st.Days = map[string]*DayStat{}
	}
	s.migrate()
	return s, nil
}

// migrate upgrades older config files in place. New fields must have a safe
// zero value so an update never resets what the parent set.
func (s *Store) migrate() {
	if s.cfg.Version < schemaVersion {
		s.cfg.Version = schemaVersion
		s.saveConfig()
	}
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// load reads path; if the file is damaged it falls back to the last good copy
// (path+".bak"), keeping the damaged one aside as path+".damaged". `how` is
// "backup" when that happened. When both are unusable the error is returned and
// the damaged file is still moved aside, so the next save cannot destroy it.
func load[T any](path string) (v T, how string, err error) {
	err = readJSON(path, &v)
	if err == nil {
		return v, "", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		os.Rename(path, path+".damaged")
	}
	var bak T
	if berr := readJSON(path+".bak", &bak); berr != nil {
		var zero T
		return zero, "", err
	}
	return bak, "backup", nil
}

// writeJSON saves atomically and keeps a copy of the last good write as path+".bak".
func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := fsx.WriteAtomic(path, b); err != nil {
		return err
	}
	fsx.WriteAtomic(path+".bak", b) // best effort: the main file is already safe
	return nil
}

func (s *Store) saveConfig() error { return writeJSON(filepath.Join(s.dir, "config.json"), s.cfg) }
func (s *Store) saveState() error  { return writeJSON(filepath.Join(s.dir, "state.json"), s.st) }

func (s *Store) Dir() string { return s.dir }

func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

// SetConfig validates, normalises and saves a new configuration.
func (s *Store) SetConfig(c Config) error {
	c, err := Validate(c)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old := s.cfg
	s.cfg = c
	if err := s.saveConfig(); err != nil {
		s.cfg = old
		return err
	}
	return nil
}

// Location is the zone schedules are evaluated in.
func (c Config) Location() *time.Location {
	if c.Timezone != "" {
		if l, err := time.LoadLocation(c.Timezone); err == nil {
			return l
		}
	}
	return time.Local
}

// Validate checks a configuration and returns it with domains normalised.
func Validate(c Config) (Config, error) {
	c.Version = schemaVersion
	if len(c.Windows) > 200 || len(c.Sites) > 3000 || len(c.AppRules) > 300 {
		return c, errors.New("too many entries")
	}
	if c.RewardCapMinutes < 0 || c.RewardCapMinutes > MaxRewardCap {
		return c, fmt.Errorf("the daily free-time limit must be between 0 and %d minutes", MaxRewardCap)
	}
	if c.Timezone != "" {
		if _, err := time.LoadLocation(c.Timezone); err != nil {
			return c, fmt.Errorf("unknown timezone %q", c.Timezone)
		}
	}
	var err error
	if c.Sites, err = sites.NormalizeAll(c.Sites); err != nil {
		return c, err
	}
	rules := map[string]bool{}
	for _, r := range c.AppRules {
		if err := r.Validate(); err != nil {
			return c, err
		}
		if rules[r.ID] {
			return c, fmt.Errorf("duplicate app id %q", r.ID)
		}
		rules[r.ID] = true
	}
	checkApps := func(ids []string) error {
		for _, id := range ids {
			if !rules[id] {
				return fmt.Errorf("unknown app %q", id)
			}
		}
		return nil
	}
	if err := checkApps(c.Apps); err != nil {
		return c, err
	}
	ids := map[string]bool{}
	ws := make([]schedule.Window, len(c.Windows))
	for i, w := range c.Windows {
		if err := w.Validate(); err != nil {
			return c, fmt.Errorf("window %q: %w", w.Name, err)
		}
		if ids[w.ID] {
			return c, fmt.Errorf("duplicate window id %q", w.ID)
		}
		ids[w.ID] = true
		if w.Sites, err = sites.NormalizeAll(w.Sites); err != nil {
			return c, err
		}
		if err := checkApps(w.Apps); err != nil {
			return c, err
		}
		ws[i] = w
	}
	c.Windows = ws
	if c.Windows == nil {
		c.Windows = []schedule.Window{}
	}
	return c, nil
}

// Rules resolves app rule ids against the library.
func (c Config) Rules(ids []string) []apps.Rule {
	byID := map[string]apps.Rule{}
	for _, r := range c.AppRules {
		byID[r.ID] = r
	}
	var out []apps.Rule
	for _, id := range ids {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out
}

// --- state --------------------------------------------------------------

func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := State{Ended: map[string]time.Time{}, Days: map[string]*DayStat{}, LastCheck: s.st.LastCheck, LastCheckResult: s.st.LastCheckResult, Rewards: s.st.Rewards.Clone()}
	for k, v := range s.st.Ended {
		cp.Ended[k] = v
	}
	for k, v := range s.st.Days {
		d := *v
		cp.Days[k] = &d
	}
	return cp
}

// Ledger is a copy of the free-time ledger (cheap, unlike State).
func (s *Store) Ledger() rewards.Ledger {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Rewards.Clone()
}

// UpdateState mutates the state under the lock and saves it.
func (s *Store) UpdateState(fn func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.st)
	if len(s.st.Days) > 400 { // keep roughly a year
		keys := make([]string, 0, len(s.st.Days))
		for k := range s.st.Days {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys[:len(keys)-400] {
			delete(s.st.Days, k)
		}
	}
	return s.saveState()
}

// Day returns the (created on demand) stats for a date key.
func (st *State) Day(key string) *DayStat {
	d := st.Days[key]
	if d == nil {
		d = &DayStat{}
		st.Days[key] = d
	}
	return d
}

// --- activity log -------------------------------------------------------

func (s *Store) logPath() string { return filepath.Join(s.dir, "activity.log") }

// Log appends one entry; it never fails the caller.
func (s *Store) Log(t time.Time, kind, msg string) {
	s.lg.Lock()
	defer s.lg.Unlock()
	if st, err := os.Stat(s.logPath()); err == nil && st.Size() > maxLogBytes {
		os.Rename(s.logPath(), s.logPath()+".1")
	}
	f, err := os.OpenFile(s.logPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(Entry{t.UTC(), kind, msg})
	f.Write(append(b, '\n'))
}

// ReadLog returns up to limit most recent entries, newest first, optionally filtered by kind.
func (s *Store) ReadLog(limit int, kinds ...string) []Entry {
	s.lg.Lock()
	defer s.lg.Unlock()
	want := map[string]bool{}
	for _, k := range kinds {
		want[k] = true
	}
	all := []Entry{} // never nil: the dashboard expects a JSON array
	for _, p := range []string{s.logPath() + ".1", s.logPath()} {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil && (len(want) == 0 || want[e.Kind]) {
				all = append(all, e)
			}
		}
		f.Close()
	}
	if len(all) > limit {
		all = all[len(all)-limit:]
	}
	for i, j := 0, len(all)-1; i < j; i, j = i+1, j-1 {
		all[i], all[j] = all[j], all[i]
	}
	return all
}
