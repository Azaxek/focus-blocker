package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Azaxek/focus-blocker/internal/rewards"
	"github.com/Azaxek/focus-blocker/internal/schedule"
	"github.com/Azaxek/focus-blocker/internal/sites"
)

func TestDefaultsAreValidAndSurviveReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(Default()); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	for cat, ds := range SitePresets {
		if _, err := sites.NormalizeAll(ds); err != nil {
			t.Errorf("preset %s: %v", cat, err)
		}
	}
	c := s.Config()
	c.Windows = []schedule.Window{{ID: "w1", Name: "School", Days: []int{1, 2}, Start: "15:30", End: "21:00"}}
	c.Sites = append(c.Sites, "https://www.Example.com/x")
	if err := s.SetConfig(c); err != nil {
		t.Fatal(err)
	}
	s.UpdateState(func(st *State) { st.Day("2026-05-04").FocusSec = 120 })
	s.Log(time.Now(), "test", "hello")

	// a new process (an updated binary) opens the same directory
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Config()
	if len(got.Windows) != 1 || got.Windows[0].Name != "School" || got.Sites[len(got.Sites)-1] != "example.com" {
		t.Fatalf("settings lost or not normalised: %+v", got.Windows)
	}
	if s2.State().Days["2026-05-04"].FocusSec != 120 {
		t.Fatal("state lost")
	}
	if l := s2.ReadLog(10); len(l) != 1 || l[0].Msg != "hello" {
		t.Fatalf("log lost: %v", l)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	good := Default()
	cases := map[string]func(*Config){
		"bad domain":  func(c *Config) { c.Sites = []string{"not a domain"} },
		"unknown app": func(c *Config) { c.Apps = []string{"nope"} },
		"dup window": func(c *Config) {
			w := schedule.Window{ID: "a", Name: "a", Days: []int{1}, Start: "09:00", End: "10:00"}
			c.Windows = []schedule.Window{w, w}
		},
		"bad window time": func(c *Config) {
			c.Windows = []schedule.Window{{ID: "a", Name: "a", Days: []int{1}, Start: "25:00", End: "10:00"}}
		},
		"bad timezone": func(c *Config) { c.Timezone = "Mars/Olympus" },
		"own app unknown": func(c *Config) {
			c.Windows = []schedule.Window{{ID: "a", Name: "a", Days: []int{1}, Start: "09:00", End: "10:00", OwnLists: true, Apps: []string{"zzz"}}}
		},
	}
	for name, mutate := range cases {
		c := good
		c.Windows = nil
		mutate(&c)
		if _, err := Validate(c); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func saveSchool(t *testing.T, dir string) {
	t.Helper()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Config()
	c.Windows = []schedule.Window{{ID: "w1", Name: "School", Days: []int{1}, Start: "15:30", End: "21:00"}}
	if err := s.SetConfig(c); err != nil {
		t.Fatal(err)
	}
}

// A service that cannot start blocks nothing, so a damaged settings file must
// never stop it: the last good copy comes back and the parent is told.
func TestDamagedConfigIsRestoredFromBackup(t *testing.T) {
	dir := t.TempDir()
	saveSchool(t, dir)
	os.WriteFile(filepath.Join(dir, "config.json"), []byte("{not json"), 0o600) // e.g. power cut mid-write
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("must still start: %v", err)
	}
	if w := s.Config().Windows; len(w) != 1 || w[0].Name != "School" {
		t.Fatalf("schedule lost: %+v", w)
	}
	if s.Notice == "" || !strings.Contains(s.Notice, "restored") {
		t.Fatalf("the parent must be told: %q", s.Notice)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.json.damaged")); string(b) != "{not json" {
		t.Fatal("the damaged file must be kept for inspection")
	}
	if l := s.ReadLog(5, "error"); len(l) == 0 {
		t.Fatal("not logged")
	}
	if _, err := Open(dir); err != nil { // and the repaired file is healthy
		t.Fatal(err)
	}
}

func TestMissingConfigComesBackFromBackup(t *testing.T) {
	dir := t.TempDir()
	saveSchool(t, dir)
	os.Remove(filepath.Join(dir, "config.json"))
	s, err := Open(dir)
	if err != nil || len(s.Config().Windows) != 1 {
		t.Fatalf("%v %+v", err, s.Config().Windows)
	}
}

func TestConfigAndBackupBothDamagedStartsFromDefaultsAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	saveSchool(t, dir)
	for _, f := range []string{"config.json", "config.json.bak"} {
		os.WriteFile(filepath.Join(dir, f), []byte("garbage"), 0o600)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("must still start: %v", err)
	}
	if len(s.Config().Windows) != 0 || len(s.Config().Sites) == 0 {
		t.Fatalf("expected the defaults: %+v", s.Config())
	}
	if !strings.Contains(s.Notice, "defaults") {
		t.Fatalf("notice: %q", s.Notice)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "config.json.damaged")); string(b) != "garbage" {
		t.Fatal("the damaged file must be kept")
	}
}

func TestDamagedStateIsResetNotFatal(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	s.UpdateState(func(st *State) { st.Day("2026-05-04").FocusSec = 5 })
	os.WriteFile(filepath.Join(dir, "state.json"), []byte("{"), 0o600)
	os.Remove(filepath.Join(dir, "state.json.bak"))
	s2, err := Open(dir)
	if err != nil || s2.Notice == "" || len(s2.State().Days) != 0 {
		t.Fatalf("%v %q %v", err, s2.Notice, s2.State().Days)
	}
}

func TestRewardsSurviveAReopen(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	now := time.Date(2026, 5, 4, 17, 0, 0, 0, time.UTC)
	s.UpdateState(func(st *State) {
		st.Rewards.Grant(rewards.Reward{ID: "r", Reason: "Gym", Seconds: 1800, Granted: now, Expires: now.Add(24 * time.Hour)})
		st.Rewards.Start(now, 600)
	})
	s2, _ := Open(dir)
	l := s2.Ledger()
	if l.Balance(now) != 1200 || !l.OnBreak(now.Add(time.Minute)) {
		t.Fatalf("%+v", l)
	}
}

func TestRewardCapValidation(t *testing.T) {
	c := Default()
	if c.RewardCap() != DefaultRewardCap {
		t.Fatal("default cap")
	}
	c.RewardCapMinutes = MaxRewardCap + 1
	if _, err := Validate(c); err == nil {
		t.Fatal("absurd cap accepted")
	}
	c.RewardCapMinutes = 90
	if v, err := Validate(c); err != nil || v.RewardCap() != 90 {
		t.Fatal(err)
	}
}

func TestLogNewestFirstAndFilter(t *testing.T) {
	s, _ := Open(t.TempDir())
	now := time.Now()
	s.Log(now, "window", "one")
	s.Log(now, "tamper", "two")
	s.Log(now, "window", "three")
	if l := s.ReadLog(10); len(l) != 3 || l[0].Msg != "three" {
		t.Fatalf("%v", l)
	}
	if l := s.ReadLog(10, "tamper"); len(l) != 1 || l[0].Msg != "two" {
		t.Fatalf("%v", l)
	}
	if l := s.ReadLog(2); len(l) != 2 || l[1].Msg != "two" {
		t.Fatalf("%v", l)
	}
}
