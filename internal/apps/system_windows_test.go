package apps

import (
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// victim starts a long-running harmless process (a copy of ping.exe) at path.
func victim(t *testing.T, path string) *exec.Cmd {
	t.Helper()
	src, err := os.Open(`C:\Windows\System32\PING.EXE`)
	if err != nil {
		t.Skip("ping.exe not available: ", err)
	}
	defer src.Close()
	os.MkdirAll(filepath.Dir(path), 0o755)
	dst, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(dst, src)
	dst.Close()
	cmd := exec.Command(path, "-t", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return cmd
}

// closedWithin scans once a second, like the service, and reports how long the process survived.
func closedWithin(t *testing.T, e *Enforcer, cmd *exec.Cmd, limit time.Duration) time.Duration {
	t.Helper()
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	start := time.Now()
	for time.Since(start) < limit {
		e.Scan()
		select {
		case <-done:
			return time.Since(start)
		case <-time.After(time.Second):
		}
	}
	t.Fatalf("process still running after %v", limit)
	return 0
}

func TestRealProcessClosedByName(t *testing.T) {
	name := fmt.Sprintf("fg-victim-%d.exe", rand.Uint32())
	cmd := victim(t, filepath.Join(t.TempDir(), name))
	e := NewEnforcer(&WinSystem{}, uint32(os.Getpid()))
	e.SetRules([]Rule{{ID: "v", Name: "Victim", Exe: []string{name}}})
	if d := closedWithin(t, e, cmd, 6*time.Second); d > 2500*time.Millisecond {
		t.Fatalf("closed after %v, the target is 2 seconds", d)
	}
}

func TestRealRenamedProcessClosedByInstallPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), fmt.Sprintf("fgtestapp%d", rand.Uint32()))
	// the file is called "homework.exe", but it lives in the blocked app's folder
	cmd := victim(t, filepath.Join(dir, "homework.exe"))
	e := NewEnforcer(&WinSystem{}, uint32(os.Getpid()))
	e.SetRules([]Rule{{ID: "v", Name: "Victim", Exe: []string{"original-name.exe"}, Path: []string{`\` + filepath.Base(dir) + `\`}}})
	if d := closedWithin(t, e, cmd, 6*time.Second); d > 2500*time.Millisecond {
		t.Fatalf("closed after %v", d)
	}
}

func TestRealUnrelatedProcessSurvives(t *testing.T) {
	cmd := victim(t, filepath.Join(t.TempDir(), fmt.Sprintf("fg-bystander-%d.exe", rand.Uint32())))
	e := NewEnforcer(&WinSystem{}, uint32(os.Getpid()))
	e.SetRules([]Rule{{ID: "v", Name: "Other", Exe: []string{"something-else.exe"}}})
	for i := 0; i < 2; i++ {
		e.Scan()
		time.Sleep(500 * time.Millisecond)
	}
	if cmd.ProcessState != nil {
		t.Fatal("an unrelated process was closed")
	}
}

func TestRealVersionInfoIsReadable(t *testing.T) {
	for _, p := range []string{`C:\Windows\System32\notepad.exe`, `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`} {
		if _, err := os.Stat(p); err == nil && originalFilename(p) != "" {
			return
		}
	}
	t.Skip("no binary with version info found")
}
