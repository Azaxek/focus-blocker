// Package auth stores the parent passcode as a salted argon2id hash and
// enforces a lockout that survives restarts. The passcode is never logged
// or written anywhere in plaintext.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/Azaxek/focus-blocker/internal/fsx"
)

const (
	MinLen      = 6
	MaxLen      = 128
	MaxFailures = 5
	LockFor     = 15 * time.Minute
)

// Params are argon2id cost settings (lowered by tests only).
var Params = struct {
	Time, Memory uint32
	Threads      uint8
}{3, 64 * 1024, 2}

var (
	ErrNotSet     = errors.New("no passcode set")
	ErrAlreadySet = errors.New("passcode already set")
	ErrShort      = fmt.Errorf("passcode must be at least %d characters", MinLen)
	ErrLong       = fmt.Errorf("passcode must be at most %d characters", MaxLen)
)

// WrongError reports a failed attempt and how many remain before lockout.
type WrongError struct{ Left int }

func (e *WrongError) Error() string { return fmt.Sprintf("wrong passcode (%d attempts left)", e.Left) }

// LockedError reports an active lockout.
type LockedError struct{ Until time.Time }

func (e *LockedError) Error() string {
	return "too many wrong attempts; locked until " + e.Until.Format("15:04")
}

type record struct {
	Hash        string    `json:"hash"`
	Failures    int       `json:"failures"`
	LockedUntil time.Time `json:"lockedUntil"`
}

type Auth struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

// New keeps its state in path; now should be the trusted clock.
func New(path string, now func() time.Time) *Auth { return &Auth{path: path, now: now} }

func (a *Auth) load() (record, error) {
	var r record
	b, err := os.ReadFile(a.path)
	if errors.Is(err, os.ErrNotExist) {
		return r, ErrNotSet
	}
	if err == nil {
		err = json.Unmarshal(b, &r)
	}
	if err == nil && r.Hash == "" {
		err = ErrNotSet
	}
	return r, err
}

func (a *Auth) save(r record) error {
	b, _ := json.Marshal(r)
	return fsx.WriteAtomic(a.path, b)
}

// IsSet reports whether a parent passcode exists.
func (a *Auth) IsSet() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, err := a.load()
	return err == nil
}

// LockedUntil returns the end of an active lockout, or zero.
func (a *Auth) LockedUntil() time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	if r, err := a.load(); err == nil && a.now().Before(r.LockedUntil) {
		return r.LockedUntil
	}
	return time.Time{}
}

func check(pass string) error {
	switch n := utf8.RuneCountInString(pass); {
	case n < MinLen:
		return ErrShort
	case n > MaxLen:
		return ErrLong
	}
	return nil
}

func hash(pass string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	p := Params
	key := argon2.IDKey([]byte(pass), salt, p.Time, p.Memory, p.Threads, 32)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func matches(encoded, pass string) bool {
	f := strings.Split(encoded, "$") // "", argon2id, v=19, m=..,t=..,p=.., salt, key
	if len(f) != 6 || f[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(f[4])
	want, err2 := base64.RawStdEncoding.DecodeString(f[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pass), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// Set creates the first passcode.
func (a *Auth) Set(pass string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.load(); err == nil {
		return ErrAlreadySet
	}
	if err := check(pass); err != nil {
		return err
	}
	h, err := hash(pass)
	if err != nil {
		return err
	}
	return a.save(record{Hash: h})
}

// Verify checks a passcode, counting failures and enforcing the lockout.
func (a *Auth) Verify(pass string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.verify(pass)
}

func (a *Auth) verify(pass string) error {
	r, err := a.load()
	if err != nil {
		return err
	}
	now := a.now()
	if now.Before(r.LockedUntil) {
		return &LockedError{r.LockedUntil}
	}
	if len(pass) <= 4*MaxLen && matches(r.Hash, pass) {
		if r.Failures != 0 {
			r.Failures = 0
			return a.save(r)
		}
		return nil
	}
	r.Failures++
	var out error = &WrongError{MaxFailures - r.Failures}
	if r.Failures >= MaxFailures {
		r.Failures, r.LockedUntil = 0, now.Add(LockFor)
		out = &LockedError{r.LockedUntil}
	}
	if err := a.save(r); err != nil { // a lockout that cannot be saved must not be skipped
		return err
	}
	return out
}

// Change replaces the passcode after verifying the old one.
func (a *Auth) Change(oldPass, newPass string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.verify(oldPass); err != nil {
		return err
	}
	if err := check(newPass); err != nil {
		return err
	}
	h, err := hash(newPass)
	if err != nil {
		return err
	}
	return a.save(record{Hash: h})
}
