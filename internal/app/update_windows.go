package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Azaxek/focus-blocker/internal/server"
	"github.com/Azaxek/focus-blocker/internal/updater"
	"github.com/Azaxek/focus-blocker/internal/winsvc"
)

const (
	detached      = 0x00000008 // DETACHED_PROCESS
	newProcGroup  = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	createNoWin   = 0x08000000
	helperTimeout = 15 * time.Minute
)

// LaunchUpdateHelper starts a detached copy of the CURRENT binary that will
// stop the service, run the installer and roll back if the new version fails.
// The copy matters: it keeps running while the install directory is replaced,
// and the rollback logic comes from the version that is known to work.
func LaunchUpdateHelper(exe, installer, from, to, dataDir string) error {
	helper := filepath.Join(dataDir, "updates", "apply-helper.exe")
	if err := copyFile(exe, helper); err != nil {
		return err
	}
	cmd := exec.Command(helper, "apply-update", "--installer", installer, "--from", from, "--to", to,
		"--install-dir", filepath.Dir(exe), "--data", dataDir)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: detached | newProcGroup | createNoWin, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	return err
}

type winSteps struct{}

func (winSteps) StopService() error  { return winsvc.Stop(45 * time.Second) }
func (winSteps) StartService() error { return winsvc.Start() }

func (winSteps) RunInstaller(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), helperTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/FGUPDATE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("installer: %v %s", err, out)
	}
	return nil
}

// Healthy waits for the service to answer with the wanted version and then
// to keep answering: a build that starts and immediately dies must not pass.
func (winSteps) Healthy(version string) bool {
	ping := func() bool {
		c := http.Client{Timeout: 2 * time.Second}
		resp, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", server.Port))
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		var p struct {
			App     string `json:"app"`
			Version string `json:"version"`
		}
		return json.NewDecoder(resp.Body).Decode(&p) == nil && p.App == "focusguard" && p.Version == version
	}
	for end := time.Now().Add(90 * time.Second); time.Now().Before(end); time.Sleep(time.Second) {
		if ping() && winsvc.Running() {
			time.Sleep(10 * time.Second)
			return ping() && winsvc.Running()
		}
	}
	return false
}

// RunUpdateHelper implements `focusguard apply-update`.
func RunUpdateHelper(installer, from, to, installDir, dataDir string) error {
	p := updater.Params{Installer: installer, InstallDir: installDir, BackupDir: filepath.Join(dataDir, "updates", "backup"), From: from, To: to}
	r := updater.RunApply(winSteps{}, p)
	err := updater.SaveResult(dataDir, r)
	os.Remove(installer)
	if !r.OK {
		return fmt.Errorf("%s", r.Message)
	}
	return err
}
