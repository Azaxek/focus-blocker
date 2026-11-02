<#
  Installs the CURRENT source tree as the real service, without the installer,
  so enforcement can be tested on this PC. Run from an ADMIN PowerShell (it
  offers to re-launch itself elevated). Undo with scripts\dev-uninstall.ps1.
#>
param([string]$Go = "$env:USERPROFILE\tools\go\bin\go.exe")

$ErrorActionPreference = 'Stop'
$id = [Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
if (-not $id.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Host "Needs administrator rights; asking Windows for them..."
    Start-Process powershell -Verb RunAs -ArgumentList "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" -Go `"$Go`""
    return
}

$root = Split-Path $PSScriptRoot -Parent
$dest = Join-Path $env:ProgramFiles 'FocusGuard'
New-Item -ItemType Directory -Force $dest | Out-Null
Push-Location $root
try {
    & $Go build -trimpath -ldflags "-X main.version=0.0.1-dev" -o (Join-Path $dest 'focusguard.exe') ./cmd/focusguard
    if ($LASTEXITCODE -ne 0) { throw "build failed" }
} finally { Pop-Location }

$exe = Join-Path $dest 'focusguard.exe'
& $exe install-service
if ($LASTEXITCODE -ne 0) { throw "install-service failed" }

# desktop shortcut + tray autostart, like the real installer
$sh = (New-Object -ComObject WScript.Shell).CreateShortcut((Join-Path ([Environment]::GetFolderPath('CommonDesktopDirectory')) 'Focus Guard.lnk'))
$sh.TargetPath = $exe; $sh.Arguments = 'app'; $sh.Save()
Set-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Run' -Name FocusGuardTray -Value "`"$exe`" tray"

Write-Host "`nInstalled. Service status:"; Get-Service FocusGuard | Format-Table Name, Status, StartType
Write-Host "Open the dashboard with the 'Focus Guard' desktop shortcut or:  & '$exe' app"
Write-Host "Remove everything with:  scripts\dev-uninstall.ps1"
Read-Host "Press Enter to close"
