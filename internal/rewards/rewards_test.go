package rewards

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"
)

var t0 = time.Date(2026, 5, 4, 17, 0, 0, 0, time.UTC)

func grant(t *testing.T, l *Ledger, id string, min int, days int) {
	t.Helper()
	if err := l.Grant(Reward{ID: id, Reason: id, Seconds: min * 60, Granted: t0, Expires: t0.AddDate(0, 0, days)}); err != nil {
		t.Fatal(err)
	}
}

func TestGrantRules(t *testing.T) {
	var l Ledger
	bad := []Reward{
		{ID: "", Seconds: 1800, Granted: t0, Expires: t0.Add(time.Hour)},
		{ID: "a", Seconds: 60, Granted: t0, Expires: t0.Add(time.Hour)},                  // under the minimum
		{ID: "a", Seconds: (MaxGrant + 1) * 60, Granted: t0, Expires: t0.Add(time.Hour)}, // over the maximum
		{ID: "a", Seconds: 1800, Granted: t0, Expires: t0},                               // already expired
	}
	for i, r := range bad {
		if l.Grant(r) == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	grant(t, &l, "a", 30, 1)
	if l.Grant(Reward{ID: "a", Seconds: 1800, Granted: t0, Expires: t0.Add(time.Hour)}) == nil {
		t.Error("duplicate id accepted")
	}
	if l.Balance(t0) != 1800 {
		t.Fatalf("balance %d", l.Balance(t0))
	}
}

func TestExpiredCreditCannotBeSpent(t *testing.T) {
	var l Ledger
	grant(t, &l, "a", 30, 1)
	late := t0.AddDate(0, 0, 1)
	if l.Balance(late) != 0 {
		t.Fatal("expired credit counted")
	}
	if _, err := l.Start(late, 60); err != ErrNoCredit {
		t.Fatalf("spent expired credit: %v", err)
	}
}

func TestSpendsSoonestToExpireFirstAndRefundsTheOtherEnd(t *testing.T) {
	var l Ledger
	grant(t, &l, "later", 30, 3)
	grant(t, &l, "soon", 30, 1)
	b, err := l.Start(t0, 40*60)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Taken) != 2 || b.Taken[0].ID != "soon" || b.Taken[0].Sec != 1800 || b.Taken[1].ID != "later" || b.Taken[1].Sec != 600 {
		t.Fatalf("draw order wrong: %+v", b.Taken)
	}
	if !b.Until.Equal(t0.Add(40 * time.Minute)) {
		t.Fatalf("until %v", b.Until)
	}
	// 10 minutes into a 40 minute break: 30 minutes come back, to the longer-lived credit first
	back, err := l.End(t0.Add(10 * time.Minute))
	if err != nil || back != 1800 {
		t.Fatalf("refund %d, %v", back, err)
	}
	if got := l.Balance(t0.Add(10 * time.Minute)); got != 60*60-600 {
		t.Fatalf("balance after refund %d", got)
	}
	if l.Items[0].Left+l.Items[1].Left != 3000 || l.Break != nil {
		t.Fatalf("%+v", l)
	}
}

func TestOnlyOneBreakAtATime(t *testing.T) {
	var l Ledger
	grant(t, &l, "a", 60, 1)
	if _, err := l.Start(t0, 1800); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Start(t0.Add(time.Minute), 600); err != ErrOnBreak {
		t.Fatalf("second break: %v", err)
	}
	if !l.OnBreak(t0.Add(29*time.Minute)) || l.OnBreak(t0.Add(30*time.Minute)) {
		t.Fatal("break window wrong")
	}
	if l.Finish(t0.Add(29 * time.Minute)) {
		t.Fatal("finished early")
	}
	if !l.Finish(t0.Add(30*time.Minute)) || l.Break != nil {
		t.Fatal("did not finish")
	}
	if _, err := l.End(t0.Add(31 * time.Minute)); err != ErrNoBreak {
		t.Fatalf("ending nothing: %v", err)
	}
}

func TestStartAndEndAtOnceGainsNothing(t *testing.T) {
	var l Ledger
	grant(t, &l, "a", 30, 1)
	l.Start(t0, 1800)
	back, _ := l.End(t0.Add(5 * time.Second))
	if back != 1795 || l.Balance(t0) != 1795 {
		t.Fatalf("refunded %d, balance %d: five seconds were spent", back, l.Balance(t0))
	}
}

func TestRevokeKeepsWhatWasSpent(t *testing.T) {
	var l Ledger
	grant(t, &l, "a", 30, 1)
	l.Start(t0, 600)
	l.Finish(t0.Add(time.Hour))
	gone, err := l.Revoke("a")
	if err != nil || gone != 1200 {
		t.Fatalf("revoked %d, %v", gone, err)
	}
	if l.Items[0].Seconds != 600 || l.Items[0].Left != 0 {
		t.Fatalf("%+v", l.Items[0])
	}
	grant(t, &l, "b", 15, 1)
	if _, err := l.Revoke("b"); err != nil || len(l.Items) != 1 {
		t.Fatal("an untouched reward should disappear entirely")
	}
	if _, err := l.Revoke("nope"); err != ErrUnknown {
		t.Fatal("unknown id")
	}
}

func TestPruneForgetsOldCredit(t *testing.T) {
	var l Ledger
	grant(t, &l, "old", 30, 1)
	grant(t, &l, "new", 30, 20)
	l.Prune(t0.AddDate(0, 0, 10))
	if len(l.Items) != 1 || l.Items[0].ID != "new" {
		t.Fatalf("%+v", l.Items)
	}
}

func TestJSONRoundTripKeepsABreak(t *testing.T) {
	var l Ledger
	grant(t, &l, "a", 30, 1)
	l.Start(t0, 900)
	b, _ := json.Marshal(l)
	var back Ledger
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.OnBreak(t0.Add(time.Minute)) || back.Break.Taken[0].Sec != 900 {
		t.Fatalf("%+v", back.Break)
	}
	c := back.Clone()
	c.Break.Taken[0].Sec = 1
	c.Items[0].Left = 1
	if back.Break.Taken[0].Sec != 900 || back.Items[0].Left == 1 {
		t.Fatal("Clone shares memory")
	}
}

// Whatever order things happen in, credit is never created, never negative,
// and never more than was granted.
func TestInvariantsUnderRandomUse(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		var l Ledger
		now := t0
		granted := 0
		for step := 0; step < 300; step++ {
			now = now.Add(time.Duration(rng.Intn(900)) * time.Second)
			switch rng.Intn(6) {
			case 0:
				m := MinGrant + rng.Intn(MaxGrant-MinGrant)
				id := string(rune('a'+step%26)) + string(rune('a'+step/26))
				if l.Grant(Reward{ID: id, Seconds: m * 60, Granted: now, Expires: now.Add(time.Duration(1+rng.Intn(72)) * time.Hour)}) == nil {
					granted += m * 60
				}
			case 1, 2:
				l.Start(now, 1+rng.Intn(3600))
			case 3:
				l.End(now)
			case 4:
				l.Finish(now)
			case 5:
				if len(l.Items) > 0 {
					l.Revoke(l.Items[rng.Intn(len(l.Items))].ID)
				}
			}
			spent, left := 0, 0
			for _, r := range l.Items {
				if r.Left < 0 || r.Left > r.Seconds || r.Seconds <= 0 {
					t.Fatalf("seed %d step %d: bad item %+v", seed, step, r)
				}
				left += r.Left
				spent += r.Seconds - r.Left
			}
			if l.Break != nil {
				sum := 0
				for _, p := range l.Break.Taken {
					sum += p.Sec
				}
				if !l.Break.Until.After(l.Break.Start) || sum != int(l.Break.Until.Sub(l.Break.Start)/time.Second) {
					t.Fatalf("seed %d step %d: break does not match what it drew: %+v", seed, step, l.Break)
				}
			}
			if left+spent > granted {
				t.Fatalf("seed %d step %d: %d seconds exist but only %d were ever granted", seed, step, left+spent, granted)
			}
		}
	}
}
