// Package rewards is the free-time ledger. A parent grants minutes (for the
// gym, homework, chores) and the child later spends them to pause a focus
// window. Credit expires, spending is drawn from the soonest-to-expire credit
// first, and ending a break early gives the unused minutes back.
//
// Everything here is plain data and arithmetic on the time it is handed, so
// the rules can be tested without a clock.
package rewards

import (
	"errors"
	"sort"
	"time"
)

const (
	MinGrant = 5   // minutes
	MaxGrant = 120 // minutes, per grant
	MaxDays  = 14  // longest a grant stays valid
	MaxItems = 500 // defensive bound on the ledger size

	// keep is how long spent or expired credit stays visible in the ledger.
	keep = 7 * 24 * time.Hour
)

// Reward is one grant of free time.
type Reward struct {
	ID      string    `json:"id"`
	Reason  string    `json:"reason"`
	Seconds int       `json:"seconds"` // granted
	Left    int       `json:"left"`    // not spent yet
	Granted time.Time `json:"granted"`
	Expires time.Time `json:"expires"`
}

// Part records how much of one reward a break drew, so ending it early can return it.
type Part struct {
	ID  string `json:"id"`
	Sec int    `json:"sec"`
}

// Break is a running pause of the focus windows.
type Break struct {
	Start time.Time `json:"start"`
	Until time.Time `json:"until"`
	Taken []Part    `json:"taken"`
}

type Ledger struct {
	Items []Reward `json:"items"`
	Break *Break   `json:"break,omitempty"`
}

var (
	ErrOnBreak   = errors.New("free time is already running")
	ErrNoBreak   = errors.New("no free time is running")
	ErrNoCredit  = errors.New("not enough free time saved up")
	ErrBadAmount = errors.New("that is not a valid amount of free time")
	ErrUnknown   = errors.New("that reward does not exist")
)

// Clone returns an independent copy.
func (l Ledger) Clone() Ledger {
	c := Ledger{Items: append([]Reward(nil), l.Items...)}
	if l.Break != nil {
		b := *l.Break
		b.Taken = append([]Part(nil), b.Taken...)
		c.Break = &b
	}
	return c
}

// Balance is the spendable seconds at now (expired credit does not count).
func (l Ledger) Balance(now time.Time) int {
	n := 0
	for _, r := range l.Items {
		if r.Expires.After(now) {
			n += r.Left
		}
	}
	return n
}

// Usable lists the credit that can still be spent at now, soonest to expire first.
func (l Ledger) Usable(now time.Time) []Reward {
	var out []Reward
	for _, r := range l.Items {
		if r.Left > 0 && r.Expires.After(now) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Expires.Before(out[j].Expires) })
	return out
}

// OnBreak reports whether a break is running at now.
func (l Ledger) OnBreak(now time.Time) bool { return l.Break != nil && now.Before(l.Break.Until) }

// Grant adds a reward. Left is set to the full amount.
func (l *Ledger) Grant(r Reward) error {
	if r.ID == "" || r.Seconds < MinGrant*60 || r.Seconds > MaxGrant*60 || !r.Expires.After(r.Granted) {
		return ErrBadAmount
	}
	for _, x := range l.Items {
		if x.ID == r.ID {
			return errors.New("duplicate reward id")
		}
	}
	if len(l.Items) >= MaxItems {
		return errors.New("too many rewards")
	}
	r.Left = r.Seconds
	l.Items = append(l.Items, r)
	return nil
}

// Revoke takes back the unspent part of a reward and returns how many seconds that was.
func (l *Ledger) Revoke(id string) (int, error) {
	for i := range l.Items {
		r := &l.Items[i]
		if r.ID != id {
			continue
		}
		gone := r.Left
		r.Seconds -= gone
		r.Left = 0
		if r.Seconds == 0 {
			l.Items = append(l.Items[:i], l.Items[i+1:]...)
		}
		return gone, nil
	}
	return 0, ErrUnknown
}

// Start draws sec seconds of credit (soonest to expire first) and begins a break.
func (l *Ledger) Start(now time.Time, sec int) (Break, error) {
	if l.OnBreak(now) {
		return Break{}, ErrOnBreak
	}
	if sec <= 0 {
		return Break{}, ErrBadAmount
	}
	if l.Balance(now) < sec {
		return Break{}, ErrNoCredit
	}
	b := Break{Start: now, Until: now.Add(time.Duration(sec) * time.Second)}
	need := sec
	for _, u := range l.Usable(now) {
		take := min(need, u.Left)
		for i := range l.Items {
			if l.Items[i].ID == u.ID {
				l.Items[i].Left -= take
			}
		}
		b.Taken = append(b.Taken, Part{u.ID, take})
		if need -= take; need == 0 {
			break
		}
	}
	l.Break = &b
	return b, nil
}

// Finish clears a break whose time is up and reports whether there was one.
func (l *Ledger) Finish(now time.Time) bool {
	if l.Break == nil || now.Before(l.Break.Until) {
		return false
	}
	l.Break = nil
	return true
}

// End stops a running break and returns the unused seconds to the credit it came from.
func (l *Ledger) End(now time.Time) (refunded int, err error) {
	if !l.OnBreak(now) {
		return 0, ErrNoBreak
	}
	unused := int(l.Break.Until.Sub(now) / time.Second)
	for i := len(l.Break.Taken) - 1; i >= 0 && unused > 0; i-- { // give back to the latest draw first
		p := l.Break.Taken[i]
		back := min(unused, p.Sec)
		for j := range l.Items {
			if l.Items[j].ID == p.ID {
				l.Items[j].Left += back
				refunded += back
				unused -= back
			}
		}
	}
	l.Break = nil
	return refunded, nil
}

// Prune forgets credit that has been spent or expired for a week.
func (l *Ledger) Prune(now time.Time) {
	kept := l.Items[:0]
	for _, r := range l.Items {
		if r.Expires.After(now.Add(-keep)) {
			kept = append(kept, r)
		}
	}
	l.Items = kept
}
