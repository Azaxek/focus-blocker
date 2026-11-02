package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Steps are the Windows-specific actions an update needs (faked in tests).
type Steps interface {
	StopService() error
	StartService() error
	RunInstaller(path string) error
	// Healthy waits until the running service reports version and stays up.
	Healthy(version string) bool
}

type Params struct {
	Installer  string
	InstallDir string
	BackupDir  string
	From, To   string
}

// Result is written next to the data so the service can log it after restart.
type Result struct {
	Time       time.Time `json:"time"`
	From       string    `json:"from"`
	To         string    `json:"to"`
	OK         bool      `json:"ok"`
	RolledBack bool      `json:"rolledBack"`
	Message    string    `json:"message"`
}

// RunApply installs an update and rolls back if the new version does not come
// up healthy. It touches only the install directory: the data directory
// (passcode, schedules, lists, logs) is never read or written here.
func RunApply(s Steps, p Params) Result {
	r := Result{Time: time.Now(), From: p.From, To: p.To}
	os.RemoveAll(p.BackupDir)
	if err := copyDir(p.InstallDir, p.BackupDir); err != nil {
		r.Message = "update not started, backup failed: " + err.Error()
		return r
	}
	err := s.StopService()
	if err == nil {
		err = s.RunInstaller(p.Installer)
	}
	if err == nil {
		err = s.StartService()
	}
	if err == nil && !s.Healthy(p.To) {
		err = errors.New("the new version did not start cleanly")
	}
	if err == nil {
		r.OK, r.Message = true, fmt.Sprintf("updated %s to %s", p.From, p.To)
		return r
	}
	// roll back: stop whatever is running, put the old files back, start it again
	s.StopService()
	rbErr := copyDir(p.BackupDir, p.InstallDir)
	startErr := s.StartService()
	r.RolledBack = true
	r.Message = fmt.Sprintf("update to %s failed (%v); restored %s", p.To, err, p.From)
	if rbErr != nil || startErr != nil {
		r.Message += fmt.Sprintf(" — restore problems: %v %v", rbErr, startErr)
	} else if !s.Healthy(p.From) {
		r.Message += " — but the old version is not answering yet"
	}
	return r
}

// copyDir copies src over dst (dst files are overwritten, extras left alone).
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, in)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		return err
	})
}

const resultFile = "update-result.json"

// SaveResult records the outcome for the service to pick up.
func SaveResult(dataDir string, r Result) error {
	b, _ := json.Marshal(r)
	return os.WriteFile(filepath.Join(dataDir, resultFile), b, 0o600)
}

// ConsumeResult returns and removes a pending update result, if any.
func ConsumeResult(dataDir string) *Result {
	p := filepath.Join(dataDir, resultFile)
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	os.Remove(p)
	var r Result
	if json.Unmarshal(b, &r) != nil {
		return nil
	}
	return &r
}
