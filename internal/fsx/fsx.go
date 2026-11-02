// Package fsx holds the two file helpers the data directory needs: crash-safe
// writes and an admin-only ACL.
package fsx

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// WriteAtomic replaces path with data; a crash leaves the old or the new file, never half of one.
func WriteAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// Lock makes dir (and everything inside it, inherited) accessible only to SYSTEM and Administrators.
func Lock(dir string) error {
	cmd := exec.Command("icacls", dir, "/inheritance:r", "/grant:r",
		"*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return &lockError{err, string(out)}
	}
	return nil
}

type lockError struct {
	err error
	out string
}

func (e *lockError) Error() string { return "icacls: " + e.err.Error() + ": " + e.out }
