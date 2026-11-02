// Command focusguard is the service, the tray helper and the admin CLI in one binary.
package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata" // schedules need zone data even on machines without it

	"github.com/Azaxek/focus-blocker/internal/app"
	"github.com/Azaxek/focus-blocker/internal/server"
	"github.com/Azaxek/focus-blocker/internal/sites"
	"github.com/Azaxek/focus-blocker/internal/tray"
	"github.com/Azaxek/focus-blocker/internal/winsvc"
)

// Set at build time: -X main.version=1.2.0
var version = "dev"

// repo is the only place updates are ever fetched from.
const repo = "Azaxek/focus-blocker"

// releasePubKey is the Ed25519 key that signs releases (see tools/keygen).
const releasePubKey = "wswYQZ/IuM7ueueJe6ycqYugZ7VgpWv9HpTbFuW8rjA="

func pubKey() ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(releasePubKey)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil // dev build: self-update disabled
	}
	return b
}

func options(dry bool) app.Options {
	return app.Options{DataDir: app.DataDir(), Version: version, Repo: repo, PubKey: pubKey(), Dry: dry}
}

func main() {
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	if winsvc.IsService() {
		cmd = "service"
	}
	var err error
	switch cmd {
	case "service":
		err = runService()
	case "run":
		err = runConsole(os.Args[2:])
	case "install-service":
		err = installService()
	case "uninstall-service":
		err = uninstallService()
	case "start-service":
		err = winsvc.Start()
	case "stop-service":
		err = winsvc.Stop(45 * time.Second)
	case "tray":
		tray.Run()
	case "app":
		tray.Launch()
	case "authorize-uninstall":
		err = authorizeUninstall()
	case "apply-update":
		err = applyUpdate(os.Args[2:])
	case "version":
		fmt.Println(version)
	default:
		fmt.Fprintln(os.Stderr, "Focus Guard", version, "\n\nusage: focusguard <run [--dry] | install-service | uninstall-service | start-service | stop-service | tray | app | authorize-uninstall | version>")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runService() error {
	var a *app.App
	return winsvc.Run(func(ctx context.Context) error {
		return app.Run(ctx, options(false), func(x *app.App) { a = x })
	}, func() { a.Resume() })
}

func runConsole(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	dry := fs.Bool("dry", false, "do not change hosts, firewall, DNS or kill processes (UI development)")
	data := fs.String("data", "", "data directory (default %ProgramData%\\FocusGuard)")
	fs.Parse(args)
	if *data != "" {
		os.Setenv("FOCUSGUARD_DATA", *data)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	fmt.Printf("Focus Guard %s â€” dashboard at http://127.0.0.1:%d/ (data: %s, dry=%v)\n", version, server.Port, app.DataDir(), *dry)
	return app.Run(ctx, options(*dry), nil)
}

func installService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return winsvc.Install(exe)
}

func uninstallService() error {
	if err := winsvc.Stop(45 * time.Second); err != nil {
		fmt.Fprintln(os.Stderr, "warning: stopping the service:", err)
	}
	// remove hosts block, DNS rules, firewall rules and browser policies
	if err := sites.NewBlocker(sites.NewSinkhole()).Uninstall(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: cleanup:", err)
	}
	return winsvc.Delete()
}

// authorizeUninstall is called by the installer before it removes or replaces
// anything: it asks the running service for approval and waits for the parent
// to enter the passcode in the dashboard.
func authorizeUninstall() error {
	if !winsvc.Running() {
		return nil // nothing is being enforced, so there is nothing to protect
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", server.Port)
	c := &http.Client{Timeout: 5 * time.Second}
	page, err := c.Get(base + "/")
	if err != nil {
		return fmt.Errorf("cannot reach the service: %w", err)
	}
	body, _ := io.ReadAll(io.LimitReader(page.Body, 1<<20))
	page.Body.Close()
	m := regexp.MustCompile(`name="fg-token" content="([0-9a-f]+)"`).FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("cannot read the service token")
	}
	call := func(method, path string) (map[string]bool, error) {
		req, _ := http.NewRequest(method, base+path, nil)
		req.Header.Set("X-FG-Token", string(m[1]))
		resp, err := c.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		var out map[string]bool
		json.NewDecoder(resp.Body).Decode(&out)
		return out, nil
	}
	if _, err := call("POST", "/api/uninstall/request"); err != nil {
		return err
	}
	tray.OpenDashboard("")
	fmt.Println("Waiting for the parent passcode in the Focus Guard windowâ€¦")
	for end := time.Now().Add(5 * time.Minute); time.Now().Before(end); time.Sleep(1500 * time.Millisecond) {
		if out, err := call("GET", "/api/uninstall/status"); err == nil && out["approved"] {
			return nil
		}
	}
	return fmt.Errorf("the parent passcode was not entered")
}

func applyUpdate(args []string) error {
	fs := flag.NewFlagSet("apply-update", flag.ExitOnError)
	inst := fs.String("installer", "", "")
	from := fs.String("from", "", "")
	to := fs.String("to", "", "")
	dir := fs.String("install-dir", "", "")
	data := fs.String("data", "", "")
	fs.Parse(args)
	if *inst == "" || *dir == "" || *data == "" || strings.TrimSpace(*to) == "" {
		return fmt.Errorf("missing arguments")
	}
	return app.RunUpdateHelper(*inst, *from, *to, *dir, *data)
}
