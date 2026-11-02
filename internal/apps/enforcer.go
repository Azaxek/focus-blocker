package apps

import (
	"os"
	"strings"
	"sync"
	"time"
)

// System is the slice of Windows the enforcer needs (faked in tests).
type System interface {
	Procs() ([]Proc, error)
	// Info returns the PE OriginalFilename and Authenticode signer of an image.
	Info(path string) (original, publisher string)
	Kill(pid uint32) error
}

type Killed struct {
	PID  uint32 `json:"pid"`
	Name string `json:"name"`
	Rule string `json:"rule"`
}

type Enforcer struct {
	// OnKill is called for every process closed.
	OnKill func(Killed)

	sys   System
	self  uint32
	mu    sync.Mutex
	rules []Rule
	info  map[string]cachedInfo
}

type cachedInfo struct {
	mod                 time.Time
	original, publisher string
}

func NewEnforcer(sys System, selfPID uint32) *Enforcer {
	return &Enforcer{sys: sys, self: selfPID, info: map[string]cachedInfo{}}
}

// SetRules replaces the active rule set (nil = nothing is blocked).
func (e *Enforcer) SetRules(rules []Rule) {
	e.mu.Lock()
	e.rules = rules
	e.mu.Unlock()
}

func (e *Enforcer) Active() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.rules) > 0
}

func (e *Enforcer) lookup(path string) (string, string) {
	st, err := os.Stat(path)
	if err != nil {
		return "", ""
	}
	if c, ok := e.info[path]; ok && c.mod.Equal(st.ModTime()) {
		return c.original, c.publisher
	}
	o, p := e.sys.Info(path)
	e.info[path] = cachedInfo{st.ModTime(), o, p}
	return o, p
}

// Scan closes every running instance of an active rule, plus its child
// processes, and returns what it closed. Call about once a second.
func (e *Enforcer) Scan() []Killed {
	e.mu.Lock()
	rules := e.rules
	e.mu.Unlock()
	if len(rules) == 0 {
		return nil
	}
	procs, err := e.sys.Procs()
	if err != nil {
		return nil
	}
	needInfo := false
	for _, r := range rules {
		needInfo = needInfo || r.needsInfo()
	}
	children := map[uint32][]Proc{}
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
	}
	var out []Killed
	dead := map[uint32]bool{}
	var kill func(p Proc, rule string)
	kill = func(p Proc, rule string) {
		if dead[p.PID] || p.PID <= 4 || p.PID == e.self || Protected(p.Name) {
			return
		}
		dead[p.PID] = true
		if e.sys.Kill(p.PID) == nil {
			k := Killed{p.PID, p.Name, rule}
			out = append(out, k)
			if e.OnKill != nil {
				e.OnKill(k)
			}
		}
		for _, c := range children[p.PID] {
			if c.PID != p.PID {
				kill(c, rule)
			}
		}
	}
	e.mu.Lock()
	for _, p := range procs {
		if dead[p.PID] || p.PID <= 4 || p.PID == e.self || Protected(p.Name) {
			continue
		}
		if needInfo && p.Path != "" && !strings.HasPrefix(strings.ToLower(p.Path), `c:\windows\`) {
			p.Original, p.Publisher = e.lookup(p.Path)
		}
		for _, r := range rules {
			if r.Match(p) {
				kill(p, r.Name)
				break
			}
		}
	}
	e.mu.Unlock()
	return out
}
