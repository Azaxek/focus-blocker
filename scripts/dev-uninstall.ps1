<#
  Removes the dev install: stops and deletes the service, removes the hosts
  block, DNS rules, firewall rules and browser policies, and deletes the files.
  Admin PowerShell (offers to re-launch itself). Does NOT ask for the passcode:
  this is the administrator's recovery path.
#>
$ErrorActionPreference = 'Continue'
$id = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $id.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`""
    return
}
$dest = Join-Path $env:ProgramFiles 'FocusGuard'
$exe = Join-Path $dest 'focusguard.exe'
if (Test-Path $exe) { & $exe uninstall-service } else { sc.exe stop FocusGuard | Out-Null; sc.exe delete FocusGuard | Out-Null }
Get-Process focusguard-tray -ErrorAction SilentlyContinue | Stop-Process -Force
Remove-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name FocusGuardTray -ErrorAction SilentlyContinue
Remove-Item (Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Focus Guard.lnk') -ErrorAction SilentlyContinue
Remove-Item $dest -Recurse -Force -ErrorAction SilentlyContinue
Remove-Item (Join-Path $env:ProgramData 'FocusGuard') -Recurse -Force -ErrorAction SilentlyContinue
ipconfig /flushdns | Out-Null
Write-Host "Removed."
Read-Host "Press Enter to close"
