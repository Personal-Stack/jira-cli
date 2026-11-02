<#
.SYNOPSIS
  Installs jira-cli on Windows and adds it to the user PATH.

.DESCRIPTION
  Downloads a release archive, verifies it against the release's SHA256SUMS,
  installs jira.exe to %LOCALAPPDATA%\Programs\jira-cli\bin (no admin rights)
  and adds that directory to the *user* PATH. Works with Windows PowerShell 5.1
  and PowerShell 7+.

  Private repository: downloads use the GitHub CLI ('gh auth login') or the
  GITHUB_TOKEN environment variable.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File install.ps1
.EXAMPLE
  .\install.ps1 -Version v0.2.0
.EXAMPLE
  .\install.ps1 -Archive .\jira-cli_0.2.0_windows_amd64.zip
.EXAMPLE
  .\install.ps1 -Uninstall
#>
[CmdletBinding()]
param(
  [string]$Version = $env:JIRA_CLI_VERSION,
  [string]$InstallDir = $(if ($env:JIRA_CLI_INSTALL_DIR) { $env:JIRA_CLI_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\jira-cli\bin' }),
  [string]$Name = 'jira',
  [string]$BaseUrl = $env:JIRA_CLI_BASE_URL,
  [string]$Archive,
  [switch]$NoModifyPath,
  [switch]$Uninstall
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'   # Invoke-WebRequest is very slow with progress on PS 5.1
$Repo = 'Personal-Stack/jira-cli'

function Info([string]$m) { Write-Host "  $m" }
function Fail([string]$m) { [Console]::Error.WriteLine("error: $m"); exit 1 }

if ($Name -notmatch '^[A-Za-z0-9._-]+$') { Fail "invalid -Name '$Name'" }
$Target = Join-Path $InstallDir "$Name.exe"

function Get-UserPath { [Environment]::GetEnvironmentVariable('Path', 'User') }
function Set-UserPath([string]$p) { [Environment]::SetEnvironmentVariable('Path', $p, 'User') }
function Split-PathList([string]$p) { if ($p) { $p.Split(';') | Where-Object { $_ -ne '' } } else { @() } }
function Test-SameDir([string]$a, [string]$b) { $a.TrimEnd('\') -ieq $b.TrimEnd('\') }

# ---------- uninstall ----------
if ($Uninstall) {
  Write-Host 'Uninstalling jira-cli'
  if (Test-Path $Target) { Remove-Item -Force $Target; Info "removed $Target" } else { Info "no binary at $Target" }
  $kept = @(Split-PathList (Get-UserPath) | Where-Object { -not (Test-SameDir $_ $InstallDir) })
  if ($kept.Count -ne @(Split-PathList (Get-UserPath)).Count) {
    Set-UserPath ($kept -join ';'); Info "removed $InstallDir from user PATH"
  }
  if ((Test-Path $InstallDir) -and -not (Get-ChildItem $InstallDir -Force | Select-Object -First 1)) {
    Remove-Item -Force $InstallDir
  }
  Write-Host 'Done. Configuration (%APPDATA%\jira-cli) and Credential Manager entries were left untouched.'
  exit 0
}

# ---------- platform ----------
$arch = $env:PROCESSOR_ARCHITECTURE
if ($env:PROCESSOR_ARCHITEW6432) { $arch = $env:PROCESSOR_ARCHITEW6432 }   # 32-bit host on 64-bit OS
switch ($arch) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { Fail "unsupported architecture: $arch (supported: AMD64, ARM64)" }
}

if ($PSVersionTable.PSVersion.Major -lt 6) {
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
}

$Tmp = Join-Path ([IO.Path]::GetTempPath()) ("jira-cli-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $Tmp | Out-Null

function Get-File([string]$Url, [string]$Out, [hashtable]$Headers = @{}) {
  if ($Url -notmatch '^https://' -and $Url -notmatch '^http://(127\.0\.0\.1|localhost)[:/]') {
    Fail "refusing non-HTTPS download URL: $Url"
  }
  Invoke-WebRequest -Uri $Url -OutFile $Out -Headers $Headers -UseBasicParsing
}

function Test-Checksum([string]$File, [string]$Sums) {
  $n = Split-Path $File -Leaf
  $line = Get-Content $Sums | Where-Object { ($_ -split '\s+')[1] -in @($n, "*$n") } | Select-Object -First 1
  if (-not $line) { Fail "$n is not listed in SHA256SUMS" }
  $want = ($line -split '\s+')[0].ToLowerInvariant()
  $got = (Get-FileHash -Algorithm SHA256 $File).Hash.ToLowerInvariant()
  if ($want -ne $got) { Fail "checksum mismatch for $n (expected $want, got $got)" }
  Info "sha256 verified: $got"
}

function Test-Gh {
  if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { return $false }
  # Native stderr + ErrorActionPreference=Stop throws on Windows PowerShell 5.1.
  $prev = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
  try { & gh auth status *> $null; return ($LASTEXITCODE -eq 0) } finally { $ErrorActionPreference = $prev }
}

try {
  # ---------- obtain archive ----------
  if ($Archive) {
    if (-not (Test-Path $Archive)) { Fail "archive not found: $Archive" }
    $asset = Split-Path $Archive -Leaf
    Copy-Item $Archive (Join-Path $Tmp $asset)
    $sums = Join-Path (Split-Path (Resolve-Path $Archive) -Parent) 'SHA256SUMS'
    if (Test-Path $sums) { Test-Checksum (Join-Path $Tmp $asset) $sums }
    else { Write-Warning 'no SHA256SUMS next to the archive; integrity not verified' }
  }
  else {
    $auth = @{}
    if ($env:GITHUB_TOKEN) { $auth = @{ Authorization = "Bearer $($env:GITHUB_TOKEN)" } }
    $useGh = (-not $BaseUrl) -and (Test-Gh)
    if (-not $Version) {
      Write-Host 'Resolving latest release'
      if ($BaseUrl) {
        Get-File "$BaseUrl/LATEST" (Join-Path $Tmp 'LATEST'); $Version = (Get-Content (Join-Path $Tmp 'LATEST') -Raw).Trim()
      } elseif ($useGh) {
        $Version = (& gh release view -R $Repo --json tagName -q .tagName).Trim()
      } else {
        try { $Version = (Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $auth).tag_name }
        catch { Fail "cannot query releases (private repository: run 'gh auth login' or set GITHUB_TOKEN)" }
      }
      if (-not $Version) { Fail 'could not determine the latest release; pass -Version' }
    }
    $asset = "jira-cli_$($Version.TrimStart('v'))_windows_$arch.zip"
    Write-Host "Downloading jira-cli $Version (windows/$arch)"
    $assetPath = Join-Path $Tmp $asset
    $sumsPath = Join-Path $Tmp 'SHA256SUMS'
    if ($BaseUrl) {
      Get-File "$BaseUrl/$Version/$asset" $assetPath
      Get-File "$BaseUrl/$Version/SHA256SUMS" $sumsPath
    } elseif ($useGh) {
      & gh release download $Version -R $Repo -D $Tmp -p $asset -p SHA256SUMS
      if ($LASTEXITCODE -ne 0) { Fail "gh release download failed" }
    } elseif ($env:GITHUB_TOKEN) {
      $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/tags/$Version" -Headers $auth
      foreach ($f in @($asset, 'SHA256SUMS')) {
        $a = $rel.assets | Where-Object { $_.name -eq $f } | Select-Object -First 1
        if (-not $a) { Fail "release $Version has no asset $f" }
        Get-File $a.url (Join-Path $Tmp $f) ($auth + @{ Accept = 'application/octet-stream' })
      }
    } else {
      $dl = "https://github.com/$Repo/releases/download/$Version"
      try { Get-File "$dl/$asset" $assetPath; Get-File "$dl/SHA256SUMS" $sumsPath }
      catch { Fail "download failed (private repository: run 'gh auth login' or set GITHUB_TOKEN)" }
    }
    Test-Checksum $assetPath $sumsPath
  }

  # ---------- install ----------
  $x = Join-Path $Tmp 'x'
  Expand-Archive -Path (Join-Path $Tmp $asset) -DestinationPath $x -Force
  $exe = Get-ChildItem -Path $x -Recurse -Filter 'jira.exe' | Select-Object -First 1
  if (-not $exe) { Fail "archive does not contain jira.exe" }
  New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
  $staged = Join-Path $InstallDir ".$Name.exe.new"
  Copy-Item $exe.FullName $staged -Force
  Move-Item -Force $staged $Target
  $installed = & $Target --version
  if ($LASTEXITCODE -ne 0) { Fail "installed binary does not run: $Target" }
  Write-Host "Installed $installed -> $Target"

  # ---------- PATH ----------
  $userPath = Get-UserPath
  $onUserPath = [bool](Split-PathList $userPath | Where-Object { Test-SameDir $_ $InstallDir })
  if (-not $onUserPath) {
    if ($NoModifyPath) {
      Write-Host "$InstallDir is not on PATH; add it to use '$Name' directly."
    } else {
      $new = (@(Split-PathList $userPath) + $InstallDir) -join ';'
      Set-UserPath $new   # also broadcasts WM_SETTINGCHANGE to new processes
      Info "added $InstallDir to the user PATH"
      Write-Host 'Open a new terminal to pick up the PATH change.'
    }
  }
  if (-not (Split-PathList $env:Path | Where-Object { Test-SameDir $_ $InstallDir })) {
    $env:Path = "$InstallDir;$env:Path"   # current session
  }

  $first = Get-Command $Name -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($first -and -not (Test-SameDir (Split-Path $first.Source -Parent) $InstallDir)) {
    Write-Warning "'$Name' resolves to $($first.Source), which shadows $Target; reinstall with -Name jira-dc or reorder PATH"
  }

  Write-Host ''
  Write-Host 'Next steps:'
  Write-Host "  $Name config validate      # create %APPDATA%\jira-cli\config.yaml first (see examples/config.yaml)"
  Write-Host "  $Name auth login           # store your personal access token in Windows Credential Manager"
  Write-Host "  $Name doctor               # check connectivity, TLS and authentication"
}
finally {
  Remove-Item -Recurse -Force $Tmp -ErrorAction SilentlyContinue
}
