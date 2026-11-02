<#
  Pulls the newest signed release from GitHub onto THIS PC, right now (administrator PowerShell).
  Same verification and rollback as the automatic updater; refuses during a focus window unless -Force.
#>
param([switch]$Force)
$id = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $id.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" $(if ($Force) { '-Force' })"
    return
}
$exe = Join-Path $env:ProgramFiles 'FocusGuard\focusguard.exe'
if (-not (Test-Path $exe)) { throw "Focus Guard is not installed at $exe" }
& $exe update-now $(if ($Force) { '--force' })
Read-Host "Press Enter to close"
