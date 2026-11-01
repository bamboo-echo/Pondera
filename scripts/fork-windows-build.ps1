#requires -Version 5.1
<#
.SYNOPSIS
  Build, reinstall and smoke-test the Pondera Windows fork without touching user data.

.DESCRIPTION
  Steps: build (scripts/desktop-build.sh) -> stop running processes -> silent uninstall
  (install directory only) -> silent install -> packaged smoke test -> launch via explorer.
  %APPDATA%\pondera and %LOCALAPPDATA%\pondera are never deleted.

.PARAMETER SkipBuild
  Reuse the existing dist/ artifacts instead of rebuilding.

.PARAMETER NoInstall
  Build only; skip uninstall/install/smoke/launch.

.PARAMETER NoLaunch
  Install and smoke-test, but do not open the app.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts/fork-windows-build.ps1
.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts/fork-windows-build.ps1 -SkipBuild
#>
param(
    [string]$Version = "",
    [string]$Channel = "mod",
    [switch]$SkipBuild,
    [switch]$NoInstall,
    [switch]$NoLaunch,
    [string]$NsisDir = ""
)

$ErrorActionPreference = "Stop"

$repo = Split-Path -Parent $PSScriptRoot
$workspace = Split-Path -Parent $repo
if (-not $NsisDir) { $NsisDir = Join-Path $workspace ".zwork\nsis\nsis-3.12" }
$logDir = Join-Path $workspace ".zwork"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$buildLog = Join-Path $logDir "fork-build.log"
$versionCounterFile = Join-Path $logDir "pondera-version.txt"

$bash = "C:\Program Files\Git\usr\bin\bash.exe"
$goBin = "C:\Program Files\Go\bin"
$installer = Join-Path $repo "dist\Pondera-windows-amd64-installer.exe"
$installRoot = Join-Path $env:LOCALAPPDATA "Programs\Pondera"
$launcher = Join-Path $installRoot "Pondera.exe"

function Write-Step([string]$Message) { Write-Host "==> $Message" -ForegroundColor Cyan }

# Every fork package gets its own patch version unless -Version names one, so
# two builds can be told apart in About/current.json and by the installer.
function Get-NextBuildVersion {
    if ($Version) { return $Version.TrimStart("v") }
    $last = [version]"0.1.0"
    if (Test-Path $versionCounterFile) {
        $raw = (Get-Content -LiteralPath $versionCounterFile -Raw).Trim()
        if ($raw) { $last = [version]$raw }
    }
    return "{0}.{1}.{2}" -f $last.Major, $last.Minor, ($last.Build + 1)
}

function Stop-Pondera {
    Get-Process -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match "Pondera|pondera" } |
        ForEach-Object { Stop-Process -Id $_.Id -Force -ErrorAction SilentlyContinue }
    Start-Sleep -Seconds 2
}

function Invoke-Build([string]$BuildVersion) {
    if (-not (Test-Path $bash)) { throw "Git Bash not found: $bash" }
    if (-not (Test-Path (Join-Path $goBin "go.exe"))) { throw "Go toolchain not found: $goBin" }
    if (-not (Test-Path (Join-Path $NsisDir "makensis.exe"))) {
        throw "NSIS not found at $NsisDir. Download nsis-3.12.zip (~2.3 MB) only after asking the user, then extract to that folder or pass -NsisDir."
    }
    $env:Path = "$goBin;$NsisDir;$env:Path"
    Write-Step "build windows/amd64 v$BuildVersion (channel $Channel)"
    $bashCommand = 'export PATH="/usr/bin:/bin:$PATH"; export ELECTRON_MIRROR="https://npmmirror.com/mirrors/electron/"; ' +
        "scripts/desktop-build.sh windows/amd64 v$BuildVersion $Channel >> ../.zwork/fork-build.log 2>&1"
    & $bash -c $bashCommand
    if ($LASTEXITCODE -ne 0) { throw "desktop-build.sh failed (exit $LASTEXITCODE); see $buildLog" }
    if (-not (Test-Path $installer)) { throw "installer missing after build: $installer" }
    if (-not $Version) { Set-Content -LiteralPath $versionCounterFile -Value $BuildVersion -NoNewline }
}

function Install-Pondera {
    if (-not (Test-Path $installer)) { throw "installer missing: $installer" }
    Stop-Pondera
    $uninstaller = Join-Path $installRoot "uninstall.exe"
    if (Test-Path $uninstaller) {
        Write-Step "silent uninstall (user data preserved)"
        Start-Process -FilePath $uninstaller -ArgumentList "/S" -Wait
        $uninstallDeadline = (Get-Date).AddSeconds(30)
        while ((Test-Path $launcher) -and ((Get-Date) -lt $uninstallDeadline)) { Start-Sleep -Seconds 1 }
    }
    Write-Step "silent install"
    Start-Process -FilePath $installer -ArgumentList "/S" -Wait
    $deadline = (Get-Date).AddSeconds(90)
    while ((Get-Date) -lt $deadline) {
        $pointer = Join-Path $installRoot "current.json"
        if ((Test-Path $launcher) -and (Test-Path $pointer)) { break }
        Start-Sleep -Seconds 3
    }
    if (-not (Test-Path $launcher)) { throw "install did not produce $launcher" }
}

function Get-ActiveInstall {
    $pointer = Join-Path $installRoot "current.json"
    if (-not (Test-Path $pointer)) { throw "current.json missing: $pointer" }
    $state = Get-Content $pointer -Raw | ConvertFrom-Json
    $activeDir = Join-Path $installRoot ([string]$state.activeDir)
    return @{
        Version = [string]$state.activeVersion
        App     = Join-Path $activeDir "app"
        Service = Join-Path $activeDir "pondera-desktop.exe"
    }
}

function Invoke-Smoke {
    $active = Get-ActiveInstall
    if (-not (Test-Path $active.App)) { throw "app bundle missing: $($active.App)" }
    if (-not (Test-Path $active.Service)) { throw "service missing: $($active.Service)" }
    Write-Step "packaged smoke ($($active.Version))"
    Push-Location $repo
    try {
        & node "desktop\packaging\smoke.mjs" $active.App --service $active.Service --timeout 90
    }
    finally { Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw "packaged smoke failed (exit $LASTEXITCODE)" }
}

if (-not $SkipBuild) {
    $buildVersion = Get-NextBuildVersion
    Invoke-Build -BuildVersion $buildVersion
} else { Write-Step "build skipped (-SkipBuild)" }

if ($NoInstall) {
    Write-Step "install skipped (-NoInstall); artifacts: dist\Pondera-windows-amd64-installer.exe"
    exit 0
}

Install-Pondera
Invoke-Smoke

if (-not $NoLaunch) {
    Write-Step "launch via explorer"
    # Give the smoke instance time to release the single-instance lock.
    Start-Sleep -Seconds 3
    Start-Process -FilePath "explorer.exe" -ArgumentList "`"$launcher`""
}

Write-Step "done. user data preserved: $env:APPDATA\pondera"
