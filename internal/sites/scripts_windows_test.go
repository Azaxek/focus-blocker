package sites

import (
	"os/exec"
	"strings"
	"testing"
)

// The scripts are only ever run elevated, so at least prove they are valid
// PowerShell (parse only, nothing is executed).
func TestGeneratedScriptsParse(t *testing.T) {
	const parse = `$s = [Console]::In.ReadToEnd(); $e = $null; [void][System.Management.Automation.Language.Parser]::ParseInput($s, [ref]$null, [ref]$e); if ($e) { $e | ForEach-Object { $_.Message }; exit 1 }`
	scripts := map[string]string{
		"nrpt":         nrptScript([]string{"youtube.com", "tiktok.com", "x.com"}),
		"nrpt-clear":   nrptScript(nil),
		"firewall":     firewallScript(true),
		"firewall-off": firewallScript(false),
		"combined":     nrptScript([]string{"a.com"}) + firewallScript(true),
	}
	for name, s := range scripts {
		cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", parse)
		cmd.Stdin = strings.NewReader(s)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("%s does not parse: %v\n%s", name, err, out)
		}
	}
}
