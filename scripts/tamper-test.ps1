<#
  Tamper test. Run during a focus window, from a NORMAL PowerShell; it asks for
  one UAC approval. As an administrator it deletes the hosts block, the browser
  "connect directly" policy and one firewall rule, and times how long the
  watchdog takes to put each one back. Then it shows the Tamper log entries.

    scripts\tamper-test.ps1
#>
param([string]$Out)

if (-not $Out) {
    $st = Invoke-RestMethod http://127.0.0.1:47820/api/status
    if (-not $st.status.blocking) { Write-Host "No focus window is active, so there is nothing to tamper with. Start a short one-off window first." -ForegroundColor Yellow; exit 2 }
    $Out = Join-Path $env:TEMP "focusguard-tamper-$PID.txt"
    Remove-Item $Out -ErrorAction SilentlyContinue
    Start-Process powershell -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Out `"$Out`""
    if (Test-Path $Out) { Get-Content $Out; Remove-Item $Out } else { Write-Host "The elevated run did not produce output (UAC declined?)" -ForegroundColor Red; exit 1 }
    exit 0
}

# ---- elevated from here; everything goes to $Out ----
function Say($m) { Add-Content $Out $m }
$hosts = "$env:SystemRoot\System32\drivers\etc\hosts"
$chrome = 'HKLM:\SOFTWARE\Policies\Google\Chrome'
$started = Get-Date

function Time-Restore($name, [scriptblock]$break, [scriptblock]$restored) {
    & $break
    $sw = [Diagnostics.Stopwatch]::StartNew()
    while ($sw.Elapsed.TotalSeconds -lt 30 -and -not (& $restored)) { Start-Sleep -Milliseconds 200 }
    if (& $restored) { Say ("  PASS  {0} restored in {1:N1}s" -f $name, $sw.Elapsed.TotalSeconds) }
    else { Say ("  FAIL  {0} NOT restored after 30s" -f $name) }
}

Say "Tamper test at $started"
Time-Restore 'hosts block' `
    { (Get-Content $hosts) | Where-Object { $_ -notmatch 'FOCUSGUARD' } | Set-Content $hosts -Encoding ASCII } `
    { (Get-Content $hosts -Raw) -match 'BEGIN FOCUSGUARD' }

Time-Restore 'Chrome connect-directly policy' `
    { Remove-ItemProperty $chrome -Name ProxyMode -ErrorAction SilentlyContinue } `
    { (Get-ItemProperty $chrome -ErrorAction SilentlyContinue).ProxyMode -eq 'direct' }

$before = @(Get-NetFirewallRule -DisplayName 'FocusGuard*').Count
Time-Restore 'firewall rule' `
    { Get-NetFirewallRule -DisplayName 'FocusGuard*' | Select-Object -First 1 | Remove-NetFirewallRule } `
    { @(Get-NetFirewallRule -DisplayName 'FocusGuard*').Count -ge $before }

Say "`nTamper entries in the activity log since the test began:"
$log = "$env:ProgramData\FocusGuard\activity.log"
$hit = Get-Content $log -ErrorAction SilentlyContinue | Where-Object { $_ -match 'tamper' } | Select-Object -Last 6
if ($hit) { $hit | ForEach-Object { Say "  $_" } } else { Say "  (none found)" }
