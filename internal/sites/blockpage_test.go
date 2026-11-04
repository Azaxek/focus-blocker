package sites

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The page a browser sees for a blocked site says what is blocked, until when, and carries a line of motivation.
func TestBlockPageShowsTheSiteTheTimeAndAQuote(t *testing.T) {
	s := NewSinkhole()
	s.Set([]string{"youtube.com"}, time.Date(2026, 5, 4, 21, 0, 0, 0, time.Local))
	req := httptest.NewRequest("GET", "/watch?v=1", nil)
	req.Host = "www.youtube.com"
	rec := httptest.NewRecorder()
	s.page().ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d", rec.Code)
	}
	for _, want := range []string{"Stay hard.", "www.youtube.com", "9:00 PM", `class="q">`} {
		if !strings.Contains(body, want) {
			t.Fatalf("page lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "%!") || strings.Contains(body, "%s") {
		t.Fatalf("page has a broken placeholder:\n%s", body)
	}
}
