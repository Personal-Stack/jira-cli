# Starts a local HTTP mirror for the installer tests and waits until it serves
# LATEST = $Version. GitHub's Windows runner kills the processes a step started
# when that step ends, so every step that needs the mirror calls this first.
param(
  [Parameter(Mandatory)] [string] $Root,
  [Parameter(Mandatory)] [string] $Version,
  [int] $Port = 8765
)
$ErrorActionPreference = 'Stop'
$log = Join-Path $env:RUNNER_TEMP "mirror-http-$PID.log"
Start-Process python -ArgumentList '-m','http.server',"$Port",'--bind','127.0.0.1','--directory',$Root `
  -WorkingDirectory $Root -RedirectStandardError $log -WindowStyle Hidden
# Download to a file and read it as text, exactly like install.ps1 does:
# LATEST has no extension, so it is served as application/octet-stream and
# Invoke-WebRequest's .Content is then a byte[] on PowerShell 7.
$probe = Join-Path $env:RUNNER_TEMP "LATEST.probe.$PID"
$ok = $false; $lastError = ''
for ($i = 0; $i -lt 30 -and -not $ok; $i++) {
  try {
    Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$Port/LATEST" -OutFile $probe
    $got = [IO.File]::ReadAllText($probe).Trim()
    $ok = ($got -eq $Version)
    if (-not $ok) { $lastError = "served '$got', expected '$Version'"; Start-Sleep 1 }
  } catch { $lastError = $_.Exception.Message; Start-Sleep 1 }
}
if (-not $ok) {
  Write-Host "last probe error: $lastError"
  Get-ChildItem -Recurse $Root | Select-Object FullName | Format-Table -AutoSize | Out-String | Write-Host
  if (Test-Path $log) { Get-Content $log | Write-Host }
  throw "local mirror did not serve $Root/LATEST"
}
Write-Host "mirror ready: $Root"
