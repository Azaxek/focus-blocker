<#
  Checks, from a NORMAL (non-admin) PowerShell, that enforcement is really on
  or off. Run it during a focus window, then again after it ends.

    scripts\verify-enforcement.ps1 -Expect Blocking
    scripts\verify-enforcement.ps1 -Expect Free
#>
param(
    [ValidateSet('Blocking', 'Free')] [string]$Expect = 'Blocking',
    [string[]]$Sites = @('youtube.com', 'www.reddit.com', 'tiktok.com'),
    [string]$VictimApp = 'notepad.exe'  # must be in the blocked-apps list as a custom app to be tested
)
$fail = 0
function Check($name, $ok) { if ($ok) { Write-Host "  PASS  $name" -ForegroundColor Green } else { Write-Host "  FAIL  $name" -ForegroundColor Red; $script:fail++ } }
$blocking = $Expect -eq 'Blocking'
Write-Host "Expecting: $Expect`n"

$st = Invoke-RestMethod http://127.0.0.1:47820/api/status
Check "service reports blocking=$blocking" ($st.status.blocking -eq $blocking)

foreach ($s in $Sites) {
    $ip = try { (Resolve-DnsName $s -Type A -ErrorAction Stop | Where-Object Type -eq A | Select-Object -First 1).IPAddress } catch { $null }
    Check "$s resolves to loopback = $blocking  (got: $ip)" ((($ip -eq '127.0.0.1')) -eq $blocking)
    $code = try { (Invoke-WebRequest "http://$s/" -UseBasicParsing -TimeoutSec 8 -MaximumRedirection 0 -ErrorAction Stop).StatusCode } catch { if ($_.Exception.Response) { [int]$_.Exception.Response.StatusCode } else { 0 } }
    if ($blocking) { Check "$s over HTTP shows the blocked page (403) (got: $code)" ($code -eq 403) }
}

$hosts = Get-Content "$env:SystemRoot\System32\drivers\etc\hosts" -Raw
Check "hosts file block present = $blocking" (($hosts -match 'BEGIN FOCUSGUARD') -eq $blocking)
$fw = @(Get-NetFirewallRule -DisplayName 'FocusGuard*' -ErrorAction SilentlyContinue).Count
Check "firewall rules present = $blocking  (count: $fw)" (($fw -ge 4) -eq $blocking)
try {
    $nrpt = @(Get-DnsClientNrptRule -ErrorAction Stop | Where-Object Comment -eq 'FocusGuard').Count
    Check "DNS (NRPT) rule present = $blocking" (($nrpt -ge 1) -eq $blocking)
} catch { Write-Host "  SKIP  DNS (NRPT) rule check needs an administrator shell" -ForegroundColor Yellow }
$pol = (Get-ItemProperty 'HKLM:\SOFTWARE\Policies\Microsoft\Edge' -ErrorAction SilentlyContinue).DnsOverHttpsMode
Check "Edge DNS-over-HTTPS policy is 'off'" ($pol -eq 'off')

if ($blocking) {
    Write-Host "`nNow open each blocked site in Chrome AND Edge: expect a connection error or the Stay-with-it page."
    Write-Host "To test apps: start a blocked program; it should close within 2 seconds and a notification should appear."
} else {
    Write-Host "`nNow open the same sites in Chrome and Edge: they should load normally."
}
Write-Host ""
if ($fail) { Write-Host "$fail check(s) failed" -ForegroundColor Red; exit 1 } else { Write-Host "All automatic checks passed" -ForegroundColor Green }
