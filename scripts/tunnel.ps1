<#
.SYNOPSIS
  Opens a Cloudflare quick tunnel (no account) to the local edge so SePay can deliver IPN callbacks.

.DESCRIPTION
  Starts `cloudflared tunnel --url http://localhost:<EdgePort>`, waits for the public
  https://*.trycloudflare.com address, prints the IPN URL to paste into the SePay dashboard and
  writes it to .tunnel/edge.url. With -Web it also tunnels the Vite dev server (redirect pages).
  The tunnel exposes the whole edge; stop it with Ctrl+C when the check or demo is over.
  The address changes on every start, so the IPN URL in the SePay dashboard must be updated.

.EXAMPLE
  powershell -ExecutionPolicy Bypass -File scripts/tunnel.ps1
  powershell -ExecutionPolicy Bypass -File scripts/tunnel.ps1 -Web
#>
param(
  [int]$EdgePort = 8080,
  [switch]$Web,
  [int]$WebPort = 5173,
  [switch]$Install
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$stateDir = Join-Path $root '.tunnel'
New-Item -ItemType Directory -Force -Path $stateDir | Out-Null

function Find-Cloudflared {
  # A shell opened before the install still has the old PATH, so re-read it and check the MSI locations too.
  $env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine') + ';' + [Environment]::GetEnvironmentVariable('Path', 'User')
  $cmd = Get-Command cloudflared -ErrorAction SilentlyContinue | Select-Object -First 1
  if ($cmd) { return $cmd.Source }
  foreach ($dir in @(${env:ProgramFiles(x86)}, $env:ProgramFiles)) {
    if (-not $dir) { continue }
    $candidate = Join-Path $dir 'cloudflared\cloudflared.exe'
    if (Test-Path $candidate) { return $candidate }
  }
  return $null
}

function Get-Cloudflared {
  $exe = Find-Cloudflared
  if ($exe) { return $exe }
  if ($Install) {
    # Out-Host: winget's output would otherwise become part of this function's return value.
    winget install --id Cloudflare.cloudflared --accept-source-agreements --accept-package-agreements | Out-Host
    $exe = Find-Cloudflared
    if ($exe) { return $exe }
  }
  throw 'cloudflared not found. Run: winget install --id Cloudflare.cloudflared  (or re-run this script with -Install)'
}

function Test-Local([int]$port, [string]$path) {
  try {
    Invoke-WebRequest -UseBasicParsing -TimeoutSec 3 -Uri "http://localhost:$port$path" | Out-Null
    return $true
  } catch {
    # Any HTTP answer (404, 401, ...) still proves something is listening.
    return [bool]$_.Exception.Response
  }
}

function Start-Tunnel([string]$exe, [string]$name, [int]$port) {
  $log = Join-Path $stateDir "$name.log"
  Remove-Item -Force -ErrorAction SilentlyContinue $log
  # cloudflared writes its banner (with the public URL) to stderr.
  $proc = Start-Process -FilePath $exe -ArgumentList @('tunnel', '--no-autoupdate', '--url', "http://localhost:$port") `
    -RedirectStandardError $log -RedirectStandardOutput (Join-Path $stateDir "$name.out") -PassThru -WindowStyle Hidden
  $deadline = (Get-Date).AddSeconds(45)
  while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
    if ($proc.HasExited) { throw "cloudflared ($name) exited early; see $log" }
    if (Test-Path $log) {
      $m = Select-String -Path $log -Pattern 'https://[a-z0-9-]+\.trycloudflare\.com' -AllMatches | Select-Object -First 1
      if ($m) {
        $url = $m.Matches[0].Value
        Set-Content -Path (Join-Path $stateDir "$name.url") -Value $url -NoNewline
        return @{ Process = $proc; Url = $url }
      }
    }
  }
  Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
  throw "No trycloudflare URL after 45 s; see $log"
}

$exe = Get-Cloudflared
if (-not (Test-Local $EdgePort '/v1/categories')) {
  Write-Warning "Nothing answers on http://localhost:$EdgePort - start the stack first (make nxl-up-all)."
}

$tunnels = @()
try {
  $edge = Start-Tunnel $exe 'edge' $EdgePort
  $tunnels += $edge.Process
  Write-Host ''
  Write-Host "Edge tunnel : $($edge.Url)" -ForegroundColor Green
  Write-Host "SePay IPN URL (dashboard, auth type SECRET_KEY):" -ForegroundColor Cyan
  Write-Host "  $($edge.Url)/v1/webhooks/sepay"

  if ($Web) {
    $webT = Start-Tunnel $exe 'web' $WebPort
    $tunnels += $webT.Process
    Write-Host "Web tunnel  : $($webT.Url)" -ForegroundColor Green
    Write-Host "Set in .env : BILLING_WEB_BASE_URL=$($webT.Url)  (restart billing afterwards)"
  }

  Write-Host ''
  Write-Host 'Check from outside:' -ForegroundColor Cyan
  Write-Host "  curl.exe -i $($edge.Url)/v1/categories"
  Write-Host ''
  Write-Host 'Tunnel is open. Press Ctrl+C to close it.' -ForegroundColor Yellow
  while ($true) {
    Start-Sleep -Seconds 2
    foreach ($p in $tunnels) {
      if ($p.HasExited) { throw 'cloudflared stopped; see .tunnel\*.log' }
    }
  }
} finally {
  foreach ($p in $tunnels) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue }
  Remove-Item -Force -ErrorAction SilentlyContinue (Join-Path $stateDir '*.url')
  Write-Host 'Tunnel closed.'
}
