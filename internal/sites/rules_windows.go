package sites

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows/registry"
)

// Known public encrypted-DNS resolvers. Browsers and apps with their own DoH
// would otherwise skip the OS resolver entirely.
var dohIPs = []string{
	"1.1.1.1", "1.0.0.1", "1.1.1.2", "1.0.0.2", "1.1.1.3", "1.0.0.3", "104.16.248.249", "104.16.249.249",
	"8.8.8.8", "8.8.4.4", "9.9.9.9", "149.112.112.112", "9.9.9.10", "149.112.112.10", "9.9.9.11", "149.112.112.11",
	"208.67.222.222", "208.67.220.220", "208.67.222.123", "208.67.220.123",
	"94.140.14.14", "94.140.15.15", "94.140.14.140", "94.140.14.141", "94.140.14.15", "94.140.15.16",
	"185.228.168.9", "185.228.169.9", "185.228.168.168", "185.228.169.168", "185.228.168.10", "185.228.169.11",
	"76.76.2.0", "76.76.10.0", "45.90.28.0", "45.90.30.0", "76.223.122.150", "13.248.141.197",
	"2606:4700:4700::1111", "2606:4700:4700::1001", "2606:4700:4700::1112", "2606:4700:4700::1002",
	"2606:4700:4700::1113", "2606:4700:4700::1003", "2606:4700::6810:f8f9", "2606:4700::6810:f9f9",
	"2001:4860:4860::8888", "2001:4860:4860::8844", "2620:fe::fe", "2620:fe::9", "2620:fe::10", "2620:fe::11",
	"2620:119:35::35", "2620:119:53::53", "2a10:50c0::ad1:ff", "2a10:50c0::ad2:ff", "2a10:50c0::1:ff", "2a10:50c0::2:ff",
	"2a0d:2a00:1::2", "2a0d:2a00:2::2",
}

const firewallRulePrefix = "FocusGuard"
const firewallRuleCount = 4

func quoteList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "@(" + strings.Join(q, ",") + ")"
}

// nrptScript routes the blocked suffixes (and the DoH canary) to the sinkhole.
func nrptScript(domains []string) string {
	var ns []string
	if len(domains) > 0 {
		ns = append(ns, canary)
		for _, d := range domains {
			ns = append(ns, d, "."+d)
		}
	}
	s := `Get-DnsClientNrptRule | Where-Object { $_.Comment -eq 'FocusGuard' } | ForEach-Object { Remove-DnsClientNrptRule -Name $_.Name -Force }` + "\n"
	if len(ns) > 0 {
		s += "Add-DnsClientNrptRule -Namespace " + quoteList(ns) + ` -NameServers '127.0.0.1' -Comment 'FocusGuard' | Out-Null` + "\n"
	}
	return s + "Clear-DnsClientCache\n"
}

func firewallScript(block bool) string {
	s := `Get-NetFirewallRule -DisplayName 'FocusGuard*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule` + "\n"
	if !block {
		return s
	}
	rule := func(name, proto, port, addr string) string {
		return fmt.Sprintf("New-NetFirewallRule -DisplayName 'FocusGuard %s' -Direction Outbound -Action Block -Protocol %s -RemotePort %s%s | Out-Null\n",
			name, proto, port, addr)
	}
	ips := " -RemoteAddress " + quoteList(dohIPs)
	return s + rule("DoH TCP", "TCP", "443", ips) + rule("DoH UDP", "UDP", "443", ips) +
		rule("DoT", "TCP", "853", "") + rule("DoQ", "UDP", "853", "")
}

func runPS(script string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Stdin = strings.NewReader("$ErrorActionPreference='Stop'\n" + script + "exit 0\n")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("powershell: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// countFirewallRules counts our enforced rules straight from the registry, which is cheap enough to poll.
func countFirewallRules() int {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\SharedAccess\Parameters\FirewallPolicy\FirewallRules`, registry.QUERY_VALUE)
	if err != nil {
		return 0
	}
	defer k.Close()
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return 0
	}
	n := 0
	for _, name := range names {
		v, _, err := k.GetStringValue(name)
		if err == nil && strings.Contains(v, firewallRulePrefix) &&
			strings.Contains(v, "|Active=TRUE|") && strings.Contains(v, "|Action=Block|") {
			n++
		}
	}
	return n
}

// probeNRPT asks the OS resolver for a random name under a blocked domain.
// Only NRPT -> sinkhole can answer that (hosts cannot), so it tests the whole path.
func probeNRPT(domain string) bool {
	name := fmt.Sprintf("fg%08x.%s", rand.Uint32(), domain)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := (&net.Resolver{}).LookupHost(ctx, name)
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if a == "127.0.0.1" {
			return true
		}
	}
	return false
}

func flushDNS() {
	cmd := exec.Command("ipconfig", "/flushdns")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Run()
}

// --- browser policies ---------------------------------------------------

var (
	polRoot = registry.LOCAL_MACHINE
	polBase = `SOFTWARE\Policies`
)

type polValue struct {
	key, name string
	dword     uint32
	str       string
	isStr     bool
}

// Encrypted DNS and QUIC are switched off; the browsers' own resolver is
// replaced by the OS one, which is where the block is.
var policies = func() []polValue {
	var p []polValue
	for _, k := range []string{`Google\Chrome`, `Microsoft\Edge`, `BraveSoftware\Brave`} {
		p = append(p,
			polValue{key: k, name: "DnsOverHttpsMode", str: "off", isStr: true},
			polValue{key: k, name: "BuiltInDnsClientEnabled", dword: 0},
			polValue{key: k, name: "QuicAllowed", dword: 0})
	}
	return append(p,
		polValue{key: `Mozilla\Firefox\DNSOverHTTPS`, name: "Enabled", dword: 0},
		polValue{key: `Mozilla\Firefox\DNSOverHTTPS`, name: "Locked", dword: 1})
}()

func setPolicies() error {
	for _, p := range policies {
		k, _, err := registry.CreateKey(polRoot, polBase+`\`+p.key, registry.SET_VALUE)
		if err != nil {
			return err
		}
		if p.isStr {
			err = k.SetStringValue(p.name, p.str)
		} else {
			err = k.SetDWordValue(p.name, p.dword)
		}
		k.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func policiesOK() bool {
	for _, p := range policies {
		k, err := registry.OpenKey(polRoot, polBase+`\`+p.key, registry.QUERY_VALUE)
		if err != nil {
			return false
		}
		var ok bool
		if p.isStr {
			v, _, err := k.GetStringValue(p.name)
			ok = err == nil && v == p.str
		} else {
			v, _, err := k.GetIntegerValue(p.name)
			ok = err == nil && uint32(v) == p.dword
		}
		k.Close()
		if !ok {
			return false
		}
	}
	return true
}

func removePolicies() {
	for _, p := range policies {
		if k, err := registry.OpenKey(polRoot, polBase+`\`+p.key, registry.SET_VALUE); err == nil {
			k.DeleteValue(p.name)
			k.Close()
		}
	}
}
