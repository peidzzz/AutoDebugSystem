<#
.SYNOPSIS
  Capture the bootloader / application console on a serial port for a while.

.EXAMPLE
  .\comlog.ps1 -Seconds 45 -Out logs\boot.log
#>
[CmdletBinding()]
param(
  [string]$Port = 'COM9',
  [int]$Baud = 115200,
  [int]$Seconds = 30,
  [string]$Out = ''
)

if ([string]::IsNullOrEmpty($Out)) {
  $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
  $Out = Join-Path (Split-Path -Parent $MyInvocation.MyCommand.Path) "..\logs\com-$stamp.log"
}
$Out = [System.IO.Path]::GetFullPath($Out)
New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Out) | Out-Null

$sp = New-Object System.IO.Ports.SerialPort $Port, $Baud, 'None', 8, 'One'
$sp.ReadTimeout = 200
$sp.DtrEnable = $false
$sp.RtsEnable = $false

$text = New-Object System.Text.StringBuilder
$flushAt = (Get-Date).AddMilliseconds(500)
try {
  $sp.Open()
  $end = (Get-Date).AddSeconds($Seconds)
  while ((Get-Date) -lt $end) {
    try { [void]$text.Append($sp.ReadExisting()) } catch { }
    Start-Sleep -Milliseconds 50

    # Flush while running so the log can be inspected before the capture ends.
    if ((Get-Date) -gt $flushAt) {
      try {
        [System.IO.File]::WriteAllText($Out, $text.ToString(), (New-Object System.Text.UTF8Encoding($false)))
      } catch { }
      $flushAt = (Get-Date).AddMilliseconds(500)
    }
  }
} finally {
  if ($sp.IsOpen) { $sp.Close() }
  $sp.Dispose()
}

[System.IO.File]::WriteAllText($Out, $text.ToString(), (New-Object System.Text.UTF8Encoding($false)))
Write-Host ("captured {0} chars -> {1}" -f $text.Length, $Out)
Write-Host '---- begin ----'
Write-Host $text.ToString()
Write-Host '---- end ----'
