// Package tray is the per-user helper: a system tray icon, desktop
// notifications, and the launcher that opens the dashboard as an app window.
// It holds no authority; it only reads public status from the service.
package tray

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/energye/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/Azaxek/focus-blocker/internal/server"
)

//go:embed icon.ico
var icon []byte

var base = fmt.Sprintf("http://127.0.0.1:%d", server.Port)

// Run shows the tray icon until the process ends. It returns at once if another copy is running.
//
// The tray runs from a per-user copy of the program so that it never holds a
// lock on the installed exe, which lets an update replace that file at any time.
func Run() {
	if exe, err := os.Executable(); err == nil {
		bin := filepath.Join(os.Getenv("LOCALAPPDATA"), "FocusGuard", "bin")
		if !strings.EqualFold(filepath.Dir(exe), bin) {
			if Running() {
				return
			}
			dst := filepath.Join(bin, "focusguard-tray.exe")
			if os.MkdirAll(bin, 0o700) == nil && copyFile(exe, dst) == nil {
				cmd := exec.Command(dst, "tray")
				cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200}
				if cmd.Start() == nil {
					cmd.Process.Release()
					return
				}
			}
		}
	}
	if !singleInstance(`Local\FocusGuardTray`) {
		return
	}
	systray.Run(onReady, func() {})
}

func copyFile(src, dst string) error {
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

func singleInstance(name string) bool {
	n, _ := windows.UTF16PtrFromString(name)
	_, err := windows.CreateMutex(nil, false, n)
	return err == nil
}

// Running reports whether a tray copy already exists in this session.
func Running() bool {
	n, _ := windows.UTF16PtrFromString(`Local\FocusGuardTray`)
	h, err := windows.OpenMutex(windows.SYNCHRONIZE, false, n)
	if err != nil {
		return false
	}
	windows.CloseHandle(h)
	return true
}

func onReady() {
	systray.SetIcon(icon)
	systray.SetTitle("Focus Guard")
	systray.SetTooltip("Focus Guard")
	state := systray.AddMenuItem("Starting…", "")
	state.Disable()
	systray.AddSeparator()
	open := systray.AddMenuItem("Open dashboard", "")
	open.Click(func() { OpenDashboard("") })
	systray.SetOnClick(func(systray.IMenu) { OpenDashboard("") })

	go poll(state)
}

type statusResp struct {
	Status struct {
		Blocking bool       `json:"blocking"`
		Until    *time.Time `json:"until"`
		Next     *struct {
			Start time.Time `json:"start"`
			Name  string    `json:"name"`
		} `json:"next"`
	} `json:"status"`
}

func get(path string, v any) error {
	c := http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get(base + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func poll(state *systray.MenuItem) {
	last := 0
	first := true
	for ; ; time.Sleep(2 * time.Second) {
		var st statusResp
		if err := get("/api/status", &st); err != nil {
			state.SetTitle("Service not responding")
			systray.SetTooltip("Focus Guard: service not responding")
			continue
		}
		line := "Free time"
		switch {
		case st.Status.Blocking && st.Status.Until != nil:
			line = "Blocking until " + st.Status.Until.Local().Format("3:04 PM")
		case st.Status.Next != nil:
			line = "Free time. Next: " + st.Status.Next.Name + " at " + st.Status.Next.Start.Local().Format("Mon 3:04 PM")
		}
		state.SetTitle(line)
		systray.SetTooltip("Focus Guard: " + line)

		var evs []struct {
			ID                int
			Kind, Title, Body string
		}
		if get(fmt.Sprintf("/api/events?after=%d", last), &evs) == nil {
			for _, e := range evs {
				last = e.ID
				if !first { // don't replay history when the tray starts
					Toast(e.Title, e.Body)
				}
			}
		}
		first = false
	}
}

func psQuote(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 {
			return ' '
		}
		return r
	}, s)
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// Toast shows a Windows notification.
func Toast(title, body string) {
	script := `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
$x = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$t = $x.GetElementsByTagName('text')
$t.Item(0).AppendChild($x.CreateTextNode(` + psQuote(title) + `)) | Out-Null
$t.Item(1).AppendChild($x.CreateTextNode(` + psQuote(body) + `)) | Out-Null
$id = '{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe'
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($id).Show([Windows.UI.Notifications.ToastNotification]::new($x))`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	go cmd.Run()
}

func browser() string {
	for _, name := range []string{"msedge.exe", "chrome.exe"} {
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+name, registry.QUERY_VALUE); err == nil {
			p, _, err := k.GetStringValue("")
			k.Close()
			if err == nil {
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// OpenDashboard opens the dashboard in a chromeless app window.
func OpenDashboard(hash string) {
	url := base + "/" + hash
	if b := browser(); b != "" {
		profile := filepath.Join(os.Getenv("LOCALAPPDATA"), "FocusGuard", "app")
		cmd := exec.Command(b, "--app="+url, "--user-data-dir="+profile, "--no-first-run", "--no-default-browser-check", "--window-size=1200,820")
		if cmd.Start() == nil {
			cmd.Process.Release()
			return
		}
	}
	exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// Launch is the desktop shortcut: make sure the tray is up, then open the window.
func Launch() {
	if !Running() {
		if exe, err := os.Executable(); err == nil {
			cmd := exec.Command(exe, "tray")
			cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x00000008 | 0x00000200}
			if cmd.Start() == nil {
				cmd.Process.Release()
			}
		}
	}
	OpenDashboard("")
}
