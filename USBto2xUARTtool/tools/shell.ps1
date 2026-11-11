<#
.SYNOPSIS
    Send commands to the letter-shell console and print the replies.

.DESCRIPTION
    Opens the virtual COM port of the console, sends one command per line and
    prints whatever comes back.  Use it to drive the shell without a terminal
    program, for example from a build script or an automated test.

.PARAMETER Port
    COM port of the console (the third virtual COM port of the board).

.PARAMETER Commands
    Commands to send, one per element.  A single string with ';' also works.

.PARAMETER WaitMs
    Time to wait for the reply of each command (default 500 ms).

.EXAMPLE
    tools\shell.cmd -Port COM10 -Commands pin pb5 out,pin read pb5
#>
[CmdletBinding()]
param(
    [string]$Port = 'COM10',
    [string[]]$Commands = @('help'),
    [int]$WaitMs = 500,
    [int]$Baud = 115200
)

$ErrorActionPreference = 'Stop'

$serial = New-Object System.IO.Ports.SerialPort $Port, $Baud, 'None', 8, 'One'
$serial.ReadTimeout = 400
$serial.WriteTimeout = 2500
$serial.DtrEnable = $false
$serial.RtsEnable = $false

try {
    $serial.Open()
    Start-Sleep -Milliseconds 200
    [void]$serial.ReadExisting()

    foreach ($command in $Commands) {
        Write-Host (">>> " + $command)
        $serial.Write($command + "`r")
        Start-Sleep -Milliseconds $WaitMs
        $reply = $serial.ReadExisting()
        if ($reply.Length -gt 0) {
            Write-Host ($reply -replace "`r`n", "`n" -replace "`r", "`n")
        }
        else {
            Write-Host "(no reply)"
        }
    }
}
finally {
    if ($serial.IsOpen) {
        $serial.Close()
    }
    $serial.Dispose()
}
