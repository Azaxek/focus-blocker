package quotes

import (
	"fmt"
	"strings"
	"testing"
)

func TestEveryLineIsUsable(t *testing.T) {
	seen := map[string]bool{}
	for k, lines := range bank {
		if len(lines) < 5 {
			t.Fatalf("kind %d has only %d lines", k, len(lines))
		}
		for _, q := range lines {
			if seen[q] {
				t.Fatalf("repeated line: %q", q)
			}
			seen[q] = true
			// a toast and a message box show roughly this much before cutting the text
			if q == "" || len(q) > 120 || strings.ContainsAny(q, "\n\r\t") {
				t.Fatalf("unusable line: %q", q)
			}
		}
	}
}

func TestRandomComesFromItsKind(t *testing.T) {
	for _, k := range []Kind{Block, Start, Done, Daily} {
		in := map[string]bool{}
		for _, q := range bank[k] {
			in[q] = true
		}
		for range 50 {
			if q := Random(k); !in[q] {
				t.Fatalf("kind %d gave %q", k, q)
			}
		}
	}
}

func TestLineOfTheDayIsStableAndChanges(t *testing.T) {
	if ForDay("2026-11-02") != ForDay("2026-11-02") {
		t.Fatal("the line of the day changed within a day")
	}
	days := map[string]bool{}
	for d := 1; d <= 28; d++ {
		days[ForDay(fmt.Sprintf("2026-11-%02d", d))] = true
	}
	if len(days) < 6 {
		t.Fatalf("only %d different lines in 28 days", len(days))
	}
}
