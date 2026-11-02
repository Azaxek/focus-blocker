package winsvc

import (
	"regexp"
	"testing"

	"golang.org/x/sys/windows"
)

// The service descriptor is what stops a standard user from stopping the service.
func TestServiceSecurityDescriptor(t *testing.T) {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatalf("SDDL does not parse: %v", err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil || dacl.AceCount != 4 {
		t.Fatalf("expected 4 entries (SYSTEM, Administrators, interactive and service users): %v %v", dacl, err)
	}
	// interactive and service users get read-only rights: no start (RP), stop (WP), pause (DT), delete (SD), or ACL edits (WD, WO)
	for _, who := range []string{"IU", "SU"} {
		m := regexp.MustCompile(`\(A;;([A-Z]+);;;` + who + `\)`).FindStringSubmatch(sddl)
		if m == nil {
			t.Fatalf("no ACE for %s", who)
		}
		for i := 0; i+2 <= len(m[1]); i += 2 {
			switch right := m[1][i : i+2]; right {
			case "RP", "WP", "DT", "SD", "WD", "WO":
				t.Errorf("%s may %s", who, right)
			}
		}
	}
}
