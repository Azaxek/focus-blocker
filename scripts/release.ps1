<#
  One command to ship an update:   scripts\release.ps1 -Version 1.2.0

  Runs the tests, tags the current commit v1.2.0, pushes it, and waits for the GitHub
  "Release" workflow to build, sign and publish the installer. Installed copies then pick
  it up by themselves within an hour (or at once with "Install latest now" in the Parent
  area, or `focusguard.exe update-now` from an administrator PowerShell).

  -DryRun   run the checks and the workflow's dry run without publishing anything.
#>
param(
    [Parameter(Mandatory = $true)][ValidatePattern('^\d+\.\d+\.\d+$')][string]$Version,
    [switch]$DryRun,
    [string]$Go = "$env:USERPROFILE\tools\go\bin\go.exe",
    [string]$Repo = 'Azaxek/focus-blocker'
)
$ErrorActionPreference = 'Stop'
Set-Location (Split-Path $PSScriptRoot -Parent)
$tag = "v$Version"

if ((git branch --show-current) -ne 'main') { throw "Release from the main branch." }
if (git status --porcelain) { throw "Commit or stash your changes first (git status is not clean)." }
if (git tag --list $tag) { throw "Tag $tag already exists." }
git fetch origin --quiet
if ((git rev-parse HEAD) -ne (git rev-parse origin/main)) { throw "Push your commits first (HEAD differs from origin/main)." }

Write-Host "Running tests..." -ForegroundColor Cyan
& $Go test ./...
if ($LASTEXITCODE -ne 0) { throw "Tests failed; nothing was released." }

if ($DryRun) {
    Write-Host "Dry run on GitHub (builds and signs, publishes nothing)..." -ForegroundColor Cyan
    gh workflow run release.yml --repo $Repo --ref main
    Start-Sleep 8
    $id = gh run list --repo $Repo --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId'
    gh run watch $id --repo $Repo --exit-status
    return
}

Write-Host "Tagging $tag and pushing..." -ForegroundColor Cyan
git tag -a $tag -m "Focus Guard $Version"
git push origin $tag
Start-Sleep 8
$id = gh run list --repo $Repo --workflow release.yml --limit 1 --json databaseId --jq '.[0].databaseId'
gh run watch $id --repo $Repo --exit-status
if ($LASTEXITCODE -ne 0) { throw "The release workflow failed. The tag $tag exists; fix the problem, then delete it with: git push origin :refs/tags/$tag; git tag -d $tag" }

Write-Host "`nPublished: https://github.com/$Repo/releases/tag/$tag" -ForegroundColor Green
Write-Host "Installed copies update by themselves within an hour. To update this PC right now (administrator PowerShell):"
Write-Host "  & `"$env:ProgramFiles\FocusGuard\focusguard.exe`" update-now"
