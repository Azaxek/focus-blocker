package auth

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() { Params.Memory, Params.Time = 8, 1 } // keep tests fast

type env struct {
	path string
	now  time.Time
}

func (e *env) auth() *Auth { return New(e.path, func() time.Time { return e.now }) }

func newEnv(t *testing.T) *env {
	return &env{filepath.Join(t.TempDir(), "auth.json"), time.Date(2026, 5, 4, 16, 0, 0, 0, time.UTC)}
}

func TestSetVerifyAndMinLength(t *testing.T) {
	e := newEnv(t)
	a := e.auth()
	if a.IsSet() {
		t.Fatal("should start unset")
	}
	if !errors.Is(a.Set("12345"), ErrShort) {
		t.Fatal("5 characters must be rejected")
	}
	if err := a.Set("correct horse"); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(a.Set("another one"), ErrAlreadySet) {
		t.Fatal("Set must not overwrite")
	}
	if err := a.Verify("correct horse"); err != nil {
		t.Fatal(err)
	}
}

func TestOnlySaltedHashIsStored(t *testing.T) {
	e := newEnv(t)
	a1, a2 := e.auth(), New(filepath.Join(t.TempDir(), "b.json"), func() time.Time { return e.now })
	a1.Set("correct horse")
	a2.Set("correct horse")
	b1, _ := os.ReadFile(e.path)
	if strings.Contains(string(b1), "correct horse") || !strings.Contains(string(b1), "$argon2id$") {
		t.Fatalf("file must hold only an argon2id hash: %s", b1)
	}
	b2, _ := os.ReadFile(a2.path)
	if string(b1) == string(b2) {
		t.Fatal("same passcode produced the same record: salt missing")
	}
}

func TestLockoutAfterFiveAndSurvivesRestart(t *testing.T) {
	e := newEnv(t)
	a := e.auth()
	a.Set("correct horse")
	for i := 1; i <= 4; i++ {
		var w *WrongError
		if err := a.Verify("nope nope"); !errors.As(err, &w) || w.Left != 5-i {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var l *LockedError
	if err := a.Verify("nope nope"); !errors.As(err, &l) {
		t.Fatalf("5th failure should lock, got %v", err)
	}
	// restart: a brand new Auth reading the same file is still locked,
	// even with the right passcode
	b := e.auth()
	if err := b.Verify("correct horse"); !errors.As(err, &l) {
		t.Fatalf("lockout lost across restart: %v", err)
	}
	e.now = e.now.Add(14*time.Minute + 59*time.Second)
	if err := e.auth().Verify("correct horse"); !errors.As(err, &l) {
		t.Fatalf("lock ended early: %v", err)
	}
	e.now = e.now.Add(2 * time.Second)
	if err := e.auth().Verify("correct horse"); err != nil {
		t.Fatalf("lock should have expired: %v", err)
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	e := newEnv(t)
	a := e.auth()
	a.Set("correct horse")
	for i := 0; i < 4; i++ {
		a.Verify("wrong wrong")
	}
	if err := a.Verify("correct horse"); err != nil {
		t.Fatal(err)
	}
	var w *WrongError
	if err := a.Verify("wrong wrong"); !errors.As(err, &w) || w.Left != 4 {
		t.Fatalf("count should have reset: %v", err)
	}
}

func TestChange(t *testing.T) {
	e := newEnv(t)
	a := e.auth()
	a.Set("correct horse")
	if err := a.Change("not it", "new passcode"); err == nil {
		t.Fatal("old passcode must be verified")
	}
	if err := a.Change("correct horse", "short"); !errors.Is(err, ErrShort) {
		t.Fatal("new passcode must meet the minimum")
	}
	if err := a.Change("correct horse", "new passcode"); err != nil {
		t.Fatal(err)
	}
	if a.Verify("correct horse") == nil || a.Verify("new passcode") != nil {
		t.Fatal("change did not take effect")
	}
}
