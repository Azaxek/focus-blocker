package tray

import "testing"

// The pop-up for a blocked site must fire for a visit and never for a background lookup,
// so it keys off what the window in front is showing.
func TestTitleHostRecognisesTheCantBeReachedPage(t *testing.T) {
	for title, want := range map[string]string{
		"www.youtube.com - Profile 1 - Microsoft​ Edge": "www.youtube.com", // the title seen on a real blocked tab
		"www.youtube.com - Google Chrome":               "www.youtube.com",
		"YouTube.com - Google Chrome":                   "youtube.com",
		"https://m.youtube.com/ - Google Chrome":        "m.youtube.com",
		// ordinary pages carry their own titles, whatever site they are on
		"YouTube - Google Chrome":                      "",
		"Homework help - Khan Academy - Google Chrome": "",
		"Search results for youtube.com - Google":      "",
		"Untitled document - Google Docs - Edge":       "",
		"New Tab - Google Chrome":                      "",
		"":                                             "",
	} {
		if got := titleHost(title); got != want {
			t.Errorf("titleHost(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestIsBlockedMatchesSubdomainsButNotLookAlikes(t *testing.T) {
	blocked := []string{"youtube.com", "ytimg.com"}
	for host, want := range map[string]bool{
		"youtube.com":      true,
		"www.youtube.com":  true,
		"m.youtube.com":    true,
		"notyoutube.com":   false,
		"youtube.com.evil": false,
		"example.com":      false,
		"khanacademy.org":  false,
	} {
		if got := isBlocked(host, blocked); got != want {
			t.Errorf("isBlocked(%q) = %v, want %v", host, got, want)
		}
	}
	if isBlocked("youtube.com", nil) {
		t.Error("nothing is blocked outside a window")
	}
}
