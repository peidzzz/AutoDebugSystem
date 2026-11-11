<#
.SYNOPSIS
  Run OpenOCD with a script file and show what it printed.

.DESCRIPTION
  OpenOCD buffers stdout when it is redirected, so commands such as 'reg' or
  'mdw' print nothing unless OpenOCD writes its own log file.  This wrapper
  always passes -c "log_output <file>" first and prints that file afterwards.

  The DAPLink on this machine only answers on the CMSIS-DAP HID backend; the
  usb_bulk backend cannot find it.  Use tools\openocd\*.cfg files that select
  'cmsis_dap_backend hid' (0.11) or 'cmsis-dap backend hid' (0.12).

.EXAMPLE
  .\ocd.ps1 -Cfg tools\openocd\_state.cfg
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$Cfg,
  [int]$TimeoutSec = 60,
  [string]$OpenOcd = ''
)

$ErrorActionPreference = 'Continue'

if (-not $OpenOcd) {
  $OpenOcd = 'D:\EmbeddedDevelopTools\openocd-20211118\OpenOCD-20211118-0.11.0\bin\openocd.exe'
}
if (-not (Test-Path $OpenOcd)) { throw "openocd not found: $OpenOcd" }
if (-not (Test-Path $Cfg)) { throw "config not found: $Cfg" }

$root = Split-Path -Parent $PSScriptRoot          # USBto2xUARTtool
$logDir = Join-Path $root 'logs'
if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Path $logDir | Out-Null }
$log = Join-Path $logDir 'openocd.log'
$err = Join-Path $logDir 'openocd.err'
foreach ($f in @($log, $err)) { if (Test-Path $f) { Remove-Item $f -Force } }

$cfgFull = (Resolve-Path $Cfg).Path
# Forward slashes: Tcl treats backslashes as escapes.
$logTcl = $log -replace '\\', '/'
$cfgTcl = $cfgFull -replace '\\', '/'

$argLine = '-c "log_output ' + $logTcl + '" -f "' + $cfgTcl + '"'
$p = Start-Process -FilePath $OpenOcd -ArgumentList $argLine -PassThru -NoNewWindow `
                   -RedirectStandardError $err -WorkingDirectory $root
if (-not $p.WaitForExit($TimeoutSec * 1000)) {
  Write-Host "(timeout after $TimeoutSec s, killing openocd)"
  try { $p.Kill() } catch { }
  $p.WaitForExit(3000) | Out-Null
}

foreach ($f in @($log, $err)) {
  if ((Test-Path $f) -and ((Get-Item $f).Length -gt 0)) {
    Write-Host "---- $([System.IO.Path]::GetFileName($f)) ----"
    Get-Content $f | Where-Object { $_ -notmatch 'DEPRECATED|Licensed under|For bug reports|http://openocd' } |
      ForEach-Object { Write-Host "  $_" }
  }
}
