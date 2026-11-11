<#
.SYNOPSIS
  Send a few commands to a serial console and print everything it answers.

.DESCRIPTION
  Handy for driving the bootloader command line on COM9 (the DAPLink virtual
  port) without a terminal.  Each command is followed by a carriage return.

.EXAMPLE
  .\term.ps1 -Commands 'bootinfo','run latest'
.EXAMPLE
  .\term.ps1 -Port COM9 -WaitMs 1500 -Commands 'ymodem-ota k'
#>
[CmdletBinding()]
param(
  [string]$Port = 'COM9',
  [int]$Baud = 115200,
  [string[]]$Commands = @(),
  [int]$WaitMs = 700,
  [int]$SettleMs = 250
)

$sp = New-Object System.IO.Ports.SerialPort $Port, $Baud, 'None', 8, 'One'
$sp.ReadTimeout = 200
$sp.DtrEnable = $false
$sp.RtsEnable = $false

$text = New-Object System.Text.StringBuilder
try {
  $sp.Open()
  Start-Sleep -Milliseconds $SettleMs
  [void]$text.Append($sp.ReadExisting())

  if ($Commands.Count -eq 0) {
    # Just listen for a moment: shows the prompt when the board is alive.
    Start-Sleep -Milliseconds $WaitMs
    [void]$text.Append($sp.ReadExisting())
  }

  foreach ($cmd in $Commands) {
    Write-Host ">>> $cmd"
    $sp.Write($cmd + "`r")
    Start-Sleep -Milliseconds $WaitMs
    [void]$text.Append($sp.ReadExisting())
  }
} finally {
  if ($sp.IsOpen) { $sp.Close() }
  $sp.Dispose()
}

Write-Host '---- begin ----'
Write-Host $text.ToString()
Write-Host '---- end ----'
