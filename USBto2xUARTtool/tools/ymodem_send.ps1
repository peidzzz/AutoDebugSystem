<#
.SYNOPSIS
    Send a .bin file to the bootloader with YModem over UART1.

.DESCRIPTION
    Drives the whole update from one serial handle: it resets the console, sends
    "ymodem-ota k" (or b), waits for the receiver, transfers the image with the
    YModem batch protocol (CRC16, 1 KiB blocks, block 0 carries the name and
    size) and prints the bootloader's verdict.

    The protocol matches F103-Bootloader/Core/Src/ymodem.c exactly:
      SOH 128 bytes / STX 1024 bytes, ACK 06, NAK 15, CAN 18, EOT 04, 'C' 43,
      CRC16-XMODEM over the payload.

.PARAMETER Port
    COM port wired to UART1 (PA9/PA10), 115200 8N1.

.PARAMETER File
    Image to send, usually dist\app-*.bin.

.PARAMETER Target
    'k' for the application slot, 'b' for the bootloader itself.

.EXAMPLE
    tools\ymodem_send.cmd -Port COM9 -File dist\app-20261026-2151.bin
#>
[CmdletBinding()]
param(
    [string]$Port = 'COM9',
    [Parameter(Mandatory = $true)][string]$File,
    [ValidateSet('k', 'b')][string]$Target = 'k',
    [int]$Baud = 115200,
    [switch]$NoPoke
)

$ErrorActionPreference = 'Stop'

function Get-Crc16 {
    param([byte[]]$Data, [int]$Offset, [int]$Count)
    $crc = 0
    for ($i = $Offset; $i -lt ($Offset + $Count); $i++) {
        $crc = $crc -bxor ([int]$Data[$i] -shl 8)
        for ($bit = 0; $bit -lt 8; $bit++) {
            if (($crc -band 0x8000) -ne 0) {
                $crc = (($crc -shl 1) -bxor 0x1021) -band 0xFFFF
            }
            else {
                $crc = ($crc -shl 1) -band 0xFFFF
            }
        }
    }
    return $crc
}

function Read-Byte {
    param($Serial, [int]$TimeoutMs = 5000)
    $deadline = (Get-Date).AddMilliseconds($TimeoutMs)
    while ((Get-Date) -lt $deadline) {
        if ($Serial.BytesToRead -gt 0) {
            return $Serial.ReadByte()
        }
        Start-Sleep -Milliseconds 5
    }
    return -1
}

function Read-Answer {
    param($Serial, [int]$TimeoutMs = 6000)
    $deadline = (Get-Date).AddMilliseconds($TimeoutMs)
    while ((Get-Date) -lt $deadline) {
        if ($Serial.BytesToRead -gt 0) {
            $b = $Serial.ReadByte()
            if (($b -eq 0x06) -or ($b -eq 0x15) -or ($b -eq 0x18)) {
                return $b
            }
        }
        else {
            Start-Sleep -Milliseconds 5
        }
    }
    return -1
}

function Send-Block {
    param($Serial, [int]$Sequence, [byte[]]$Payload, [int]$Length, [int]$Attempts = 6)

    $header = if ($Length -eq 128) { 0x01 } else { 0x02 }
    $frame = New-Object byte[] (3 + $Length + 2)
    $frame[0] = $header
    $frame[1] = [byte]($Sequence -band 0xFF)
    $frame[2] = [byte]((0xFF - $Sequence) -band 0xFF)
    [Array]::Copy($Payload, 0, $frame, 3, $Length)
    $crc = Get-Crc16 -Data $Payload -Offset 0 -Count $Length
    $frame[3 + $Length] = [byte](($crc -shr 8) -band 0xFF)
    $frame[4 + $Length] = [byte]($crc -band 0xFF)

    for ($attempt = 1; $attempt -le $Attempts; $attempt++) {
        $Serial.Write($frame, 0, $frame.Length)
        $answer = Read-Answer -Serial $Serial -TimeoutMs 6000
        if ($answer -eq 0x06) { return $true }
        if ($answer -eq 0x18) { throw "the receiver cancelled the transfer" }
        if ($answer -eq 0x15) {
            Write-Host ("  block {0}: NAK, retry {1}" -f $Sequence, $attempt)
            continue
        }
        Write-Host ("  block {0}: timeout, retry {1}" -f $Sequence, $attempt)
    }
    return $false
}

if (-not (Test-Path -LiteralPath $File)) {
    throw "file not found: $File"
}

$image = [System.IO.File]::ReadAllBytes((Resolve-Path -LiteralPath $File))
$name = [System.IO.Path]::GetFileName($File)
Write-Host ("image    : {0} ({1} bytes)" -f $name, $image.Length)

$serial = New-Object System.IO.Ports.SerialPort $Port, $Baud, 'None', 8, 'One'
$serial.ReadTimeout = 100
$serial.WriteTimeout = 5000
$serial.DtrEnable = $false
$serial.RtsEnable = $false
$serial.ReadBufferSize = 65536

try {
    $serial.Open()
    Start-Sleep -Milliseconds 200
    [void]$serial.ReadExisting()

    # Cancel any transfer a previous run may have left hanging: the receiver
    # treats a CAN byte as an abort and returns to the command line.
    $serial.Write([byte[]]@(0x18, 0x18, 0x18, 0x0D, 0x0D, 0x0D, 0x0D), 0, 7)
    Start-Sleep -Milliseconds 600
    [void]$serial.ReadExisting()

    if (-not $NoPoke) {
        Write-Host "aborting the boot countdown ..."
        $deadline = (Get-Date).AddSeconds(6)
        while ((Get-Date) -lt $deadline) {
            try { $serial.Write("`r") } catch { }
            Start-Sleep -Milliseconds 250
            $text = $serial.ReadExisting()
            if ($text -match 'letter:/') { break }
        }
        Start-Sleep -Milliseconds 200
        [void]$serial.ReadExisting()
    }

    Write-Host ("sending 'ymodem-ota {0}' ..." -f $Target)
    $serial.Write("ymodem-ota $Target`r")

    # Wait until the bootloader has printed its preamble.  A bare 'C' is not
    # enough: the prompt echo and the dot line can contain one, and sending the
    # header too early made the bytes be parsed as a shell command.
    Write-Host "waiting for the receiver ..."
    $seen = $false
    $deadline = (Get-Date).AddSeconds(20)
    $banner = ''
    while ((-not $seen) -and ((Get-Date) -lt $deadline)) {
        $b = Read-Byte -Serial $serial -TimeoutMs 500
        if ($b -lt 0) { continue }
        if ($b -ge 0x20) {
            $banner += [char]$b
            if ($banner -match 'send the \.bin within') { $seen = $true }
            if ($banner.Length -gt 4000) { $banner = $banner.Substring($banner.Length - 500) }
        }
    }
    if (-not $seen) {
        throw ("the bootloader did not start a transfer: " + ($banner -replace '\s+', ' '))
    }

    # The receiver now emits one 'C' per second; the next one is our cue.
    $got = -1
    $deadline = (Get-Date).AddSeconds(8)
    while ((Get-Date) -lt $deadline) {
        $b = Read-Byte -Serial $serial -TimeoutMs 500
        if ($b -eq 0x43) { $got = $b; break }
    }
    if ($got -ne 0x43) { throw "no 'C' from the receiver after the preamble" }

    # Block 0: file name, then the size in decimal.
    $header = New-Object byte[] 128
    $nameBytes = [System.Text.Encoding]::ASCII.GetBytes($name)
    [Array]::Copy($nameBytes, 0, $header, 0, $nameBytes.Length)
    $sizeBytes = [System.Text.Encoding]::ASCII.GetBytes($image.Length.ToString())
    [Array]::Copy($sizeBytes, 0, $header, $nameBytes.Length + 1, $sizeBytes.Length)

    Write-Host "sending the header block ..."
    if (-not (Send-Block -Serial $serial -Sequence 0 -Payload $header -Length 128)) {
        throw "the receiver did not accept the header block"
    }

    # The receiver answers the header with 'C' when it is ready for data.
    $ready = $false
    $deadline = (Get-Date).AddSeconds(15)
    while ((-not $ready) -and ((Get-Date) -lt $deadline)) {
        $b = Read-Byte -Serial $serial -TimeoutMs 1000
        if ($b -eq 0x43) { $ready = $true }
    }
    if (-not $ready) { throw "the receiver never asked for data after the header" }

    $blockSize = 1024
    $total = $image.Length
    $offset = 0
    $sequence = 1
    $started = Get-Date
    while ($offset -lt $total) {
        $count = [Math]::Min($blockSize, $total - $offset)
        $payload = New-Object byte[] $blockSize
        [Array]::Copy($image, $offset, $payload, 0, $count)
        for ($i = $count; $i -lt $blockSize; $i++) { $payload[$i] = 0x1A }

        if (-not (Send-Block -Serial $serial -Sequence $sequence -Payload $payload -Length $blockSize)) {
            throw ("block {0} failed" -f $sequence)
        }
        $offset += $count
        $sequence++
        if (($sequence % 8) -eq 0 -or $offset -ge $total) {
            $percent = [int](100 * $offset / $total)
            Write-Host ("  {0,3}%  {1} / {2} bytes" -f $percent, $offset, $total)
        }
    }

    Write-Host "sending EOT ..."
    $serial.Write([byte[]]@(0x04), 0, 1)
    $b = Read-Byte -Serial $serial -TimeoutMs 8000
    if ($b -ne 0x06) { throw ("EOT was not acknowledged (0x{0:x2})" -f $b) }

    # Final empty block 0 closes the batch.
    $b = Read-Byte -Serial $serial -TimeoutMs 8000
    if ($b -eq 0x43) {
        $empty = New-Object byte[] 128
        if (-not (Send-Block -Serial $serial -Sequence 0 -Payload $empty -Length 128)) {
            throw "the receiver did not accept the closing block"
        }
    }

    $elapsed = ((Get-Date) - $started).TotalSeconds
    Write-Host ("transfer finished in {0:N1} s, waiting for the verdict ..." -f $elapsed)
    Start-Sleep -Milliseconds 1500
    Write-Host ($serial.ReadExisting())
}
finally {
    if ($serial.IsOpen) { $serial.Close() }
    $serial.Dispose()
}
