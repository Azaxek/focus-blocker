// Package apps identifies and terminates blocked desktop applications.
//
// A process matches a rule when ANY of these agree, so renaming the file is
// not enough to dodge it: the image name, a fragment of its install path, the
// PE OriginalFilename, the Authenticode signer, or a Store package folder.
package apps

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Rule struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Exe       []string `json:"exe,omitempty"`       // image names, e.g. steam.exe
	Original  []string `json:"original,omitempty"`  // PE OriginalFilename, survives renaming
	Path      []string `json:"path,omitempty"`      // install-path fragments, e.g. \steam\
	Publisher []string `json:"publisher,omitempty"` // Authenticode signer substring
	Package   []string `json:"package,omitempty"`   // Store package folder prefix under \WindowsApps\
}

type Proc struct {
	PID, PPID uint32
	Name      string // image name
	Path      string // full image path, "" if unreadable
	Original  string // PE OriginalFilename (filled lazily)
	Publisher string // signer (filled lazily)
}

// protected processes can never be named by a rule or killed.
var protected = map[string]bool{
	"system": true, "smss.exe": true, "csrss.exe": true, "wininit.exe": true, "winlogon.exe": true,
	"services.exe": true, "lsass.exe": true, "svchost.exe": true, "explorer.exe": true, "dwm.exe": true,
	"fontdrvhost.exe": true, "registry": true, "memory compression": true, "taskmgr.exe": true,
	"focusguard.exe": true, "msedge.exe": true, "chrome.exe": true, "firefox.exe": true,
	"powershell.exe": true, "cmd.exe": true, "conhost.exe": true, "wmiprvse.exe": true,
}

func Protected(name string) bool { return protected[strings.ToLower(name)] }

func (r Rule) needsInfo() bool { return len(r.Original) > 0 || len(r.Publisher) > 0 }

func contains(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Match reports whether p is an instance of the rule.
func (r Rule) Match(p Proc) bool {
	if contains(r.Exe, p.Name) {
		return true
	}
	lp := strings.ToLower(p.Path)
	if lp != "" {
		for _, f := range r.Path {
			if strings.Contains(lp, strings.ToLower(f)) {
				return true
			}
		}
		for _, pk := range r.Package {
			if strings.Contains(lp, `\windowsapps\`+strings.ToLower(pk)) {
				return true
			}
		}
	}
	if p.Original != "" && contains(r.Original, p.Original) {
		return true
	}
	if lpub := strings.ToLower(p.Publisher); lpub != "" {
		for _, s := range r.Publisher {
			if strings.Contains(lpub, strings.ToLower(s)) {
				return true
			}
		}
	}
	return false
}

var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func (r Rule) Validate() error {
	if !idRe.MatchString(r.ID) {
		return fmt.Errorf("bad app id %q", r.ID)
	}
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 80 {
		return errors.New("app needs a name")
	}
	if len(r.Exe)+len(r.Original)+len(r.Path)+len(r.Publisher)+len(r.Package) == 0 {
		return errors.New("app needs at least one way to be recognised")
	}
	for _, e := range append(append([]string{}, r.Exe...), r.Original...) {
		if !strings.HasSuffix(strings.ToLower(e), ".exe") || strings.ContainsAny(e, `\/:`) || len(e) > 80 {
			return fmt.Errorf("%q is not an executable name like game.exe", e)
		}
		if Protected(e) {
			return fmt.Errorf("%s is protected and cannot be blocked", e)
		}
	}
	for _, f := range r.Path {
		if len(f) < 5 || len(f) > 200 || !strings.Contains(f, `\`) {
			return fmt.Errorf("path fragment %q is too short or has no backslash", f)
		}
	}
	for _, s := range append(append([]string{}, r.Publisher...), r.Package...) {
		if len(s) < 4 || len(s) > 100 {
			return fmt.Errorf("%q is too short to be specific", s)
		}
	}
	return nil
}
