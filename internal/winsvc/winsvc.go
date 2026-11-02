// Package winsvc runs the program as a Windows service and manages its
// registration: auto-start at boot, restart on any failure, and a security
// descriptor that gives standard users read-only access (no stop, no pause).
package winsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	Name        = "FocusGuard"
	DisplayName = "Focus Guard"
	Description = "Enforces the scheduled focus windows (website and app blocking). Managed with a parent passcode."

	// SYSTEM and Administrators may start/stop; interactive and service users may only read status.
	sddl = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWLOCRRC;;;IU)(A;;CCLCSWLOCRRC;;;SU)"
)

// IsService reports whether the process was started by the service manager.
func IsService() bool {
	ok, _ := svc.IsWindowsService()
	return ok
}

type handler struct {
	run      func(ctx context.Context) error
	onResume func()
}

// Run executes run as the service body. run must return nil after ctx is cancelled.
func Run(run func(ctx context.Context) error, onResume func()) error {
	return svc.Run(Name, &handler{run, onResume})
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPowerEvent}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.PowerEvent:
				if (c.EventType == 7 || c.EventType == 18) && h.onResume != nil { // PBT_APMRESUMESUSPEND / AUTOMATIC
					h.onResume()
				}
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		case err := <-done:
			if err != nil { // crashed: a non-zero exit makes the service manager restart us
				return true, 1
			}
			return false, 0 // passcode-approved stop
		}
	}
}

// Install registers and starts the service. Safe to run again (repairs the settings).
func Install(exe string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		s, err = m.CreateService(Name, exe, mgr.Config{
			DisplayName: DisplayName, Description: Description,
			StartType: mgr.StartAutomatic, ErrorControl: mgr.ErrorNormal, ServiceType: windows.SERVICE_WIN32_OWN_PROCESS,
		}, "service")
		if err != nil {
			return err
		}
	} else {
		c, cerr := s.Config()
		if cerr == nil {
			c.BinaryPathName = `"` + exe + `" service`
			c.StartType, c.DisplayName, c.Description = mgr.StartAutomatic, DisplayName, Description
			cerr = s.UpdateConfig(c)
		}
		if cerr != nil {
			s.Close()
			return cerr
		}
	}
	defer s.Close()
	if err := s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Second},
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
	}, 3600); err != nil {
		return err
	}
	if err := s.SetRecoveryActionsOnNonCrashFailures(true); err != nil {
		return err
	}
	if err := lockdown(); err != nil {
		return fmt.Errorf("securing service: %w", err)
	}
	safeBoot(true)
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return nil
}

// safeBoot lists the service for Safe Mode (minimal and with networking), so
// restarting into Safe Mode does not switch enforcement off.
func safeBoot(add bool) {
	for _, mode := range []string{"Minimal", "Network"} {
		path := `SYSTEM\CurrentControlSet\Control\SafeBoot\` + mode + `\` + Name
		if add {
			if k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE); err == nil {
				k.SetStringValue("", "Service")
				k.Close()
			}
		} else {
			registry.DeleteKey(registry.LOCAL_MACHINE, path)
		}
	}
}

// lockdown sets the service DACL.
func lockdown() error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(Name, windows.SE_SERVICE,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// Installed reports whether the service is registered.
func Installed() bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return false
	}
	s.Close()
	return true
}

// Stop stops the service and waits for it to exit.
func Stop(timeout time.Duration) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return nil // nothing to stop
	}
	defer s.Close()
	status, err := s.Control(svc.Stop)
	if err != nil {
		if q, qerr := s.Query(); qerr == nil && q.State == svc.Stopped {
			return nil
		}
		return err
	}
	for end := time.Now().Add(timeout); status.State != svc.Stopped; {
		if time.Now().After(end) {
			return errors.New("timed out waiting for the service to stop")
		}
		time.Sleep(300 * time.Millisecond)
		if status, err = s.Query(); err != nil {
			return err
		}
	}
	return nil
}

// Start starts the service (already running counts as success).
func Start() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return nil
}

// Running reports whether the service is in the running state.
func Running() bool {
	m, err := mgr.Connect()
	if err != nil {
		return false
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return false
	}
	defer s.Close()
	q, err := s.Query()
	return err == nil && q.State == svc.Running
}

// Delete removes the service registration (stop it first).
func Delete() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(Name)
	if err != nil {
		return nil
	}
	defer s.Close()
	safeBoot(false)
	return s.Delete()
}
