<#
.SYNOPSIS
    Catch the bootloader console on UART1 and drive it.

.DESCRIPTION
    The bootloader waits three seconds after reset for any key and then starts
    the application.  This helper opens the UART1 port (the debug probe's
    virtual COM port), keeps sending a harmless key for a while so the
    countdown is aborted, and prints everything the bootloader says.

.PARAMETER Port
    COM port wired to UART1 (PA9/PA10).

.PARAMETER Seconds
    How long to keep poking the key.

.PARAMETER Commands
    Commands to send after the console is up.

.EXAMPLE
    tools\bootshell.ps1 -Port COM9 -Seconds 12 -Commands bootinfo
#>
[CmdletBinding()]
param(
    [string]$Port = 'COM9',
    [int]$Seconds = 12,
    [string[]]$Commands = @(),
    [int]$Baud = 115200,
    [int]$WaitMs = 800
)

$ErrorActionPreference = 'Stop'

$serial = New-Object System.IO.Ports.SerialPort $Port, $Baud, 'None', 8, 'One'
$serial.ReadTimeout = 200
$serial.WriteTimeout = 1000
$serial.DtrEnable = $false
$serial.RtsEnable = $false

$text = ''
try {
    $serial.Open()
    $deadline = (Get-Date).AddSeconds($Seconds)
    while ((Get-Date) -lt $deadline) {
        try {
            $serial.Write(" ")
        }
        catch {
            # The port may briefly refuse writes while the board resets.
        }
        Start-Sleep -Milliseconds 250
        $text += $serial.ReadExisting()
    }

    foreach ($command in $Commands) {
        Write-Host (">>> " + $command)
        $serial.Write($command + "`r")
        Start-Sleep -Milliseconds $WaitMs
        Write-Host ($serial.ReadExisting())
    }
}
finally {
    if ($serial.IsOpen) {
        $serial.Close()
    }
    $serial.Dispose()
}

Write-Host "---- captured ----"
Write-Host (($text -replace "`r`n", "`n" -replace "`r", "`n"))
