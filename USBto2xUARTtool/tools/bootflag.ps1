<#
.SYNOPSIS
  Build a 32-byte bootloader flag record for an application .bin.

.DESCRIPTION
  The bootloader only starts the application when the flag page at 0x08005800
  holds a valid record whose image_crc matches the image.  A normal update gets
  there through 'ymodem-ota k'; this script produces the same record offline so
  a freshly flashed image can be started without a serial transfer - useful when
  only SWD is available.

  Record layout (BootFlag in F103-Bootloader/Core/Inc/boot.h):
    magic 0x424F4F54 | version 2 | seq | state 0 (IDLE) | slot 0
    image_size | image_crc (Boot_ImageCrc) | crc (over the seven words above)

  Note the two different CRCs: image_crc is Boot_Crc32(...) XOR 0xFFFFFFFF,
  while the record's own crc is the raw Boot_Crc32 over 28 bytes with no final
  xor.

.EXAMPLE
  .\bootflag.ps1 -Bin ..\dist\app-20261025-2152.bin -Out ..\dist\flag.bin
#>
[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$Bin,
  [string]$Out = '',
  [uint32]$Seq = 1
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $Bin)) { throw "no such file: $Bin" }

# Same algorithm as Boot_Crc32(): MSB-first, poly 0x04C11DB7, init 0xFFFFFFFF.
function Get-BootCrc32 {
  param([byte[]]$Data, [uint32]$Init = [uint32]::MaxValue)

  $poly = [uint32]0x04C11DB7
  $highBit = [uint32]2147483648
  if ($null -eq $script:crcTable) {
    $script:crcTable = New-Object uint32[] 256
    for ($i = 0; $i -lt 256; $i++) {
      $c = [uint32]([uint32]$i -shl 24)
      for ($b = 0; $b -lt 8; $b++) {
        if ($c -band $highBit) { $c = [uint32](([uint32]($c -shl 1)) -bxor $poly) }
        else { $c = [uint32]($c -shl 1) }
      }
      $script:crcTable[$i] = $c
    }
  }

  $crc = $Init
  foreach ($byte in $Data) {
    $index = ((($crc -shr 24) -bxor $byte) -band 0xFF)
    $crc = [uint32](([uint32]($crc -shl 8)) -bxor $script:crcTable[$index])
  }
  return [uint32]$crc
}

$image = [System.IO.File]::ReadAllBytes((Resolve-Path $Bin).Path)
$imageCrc = (Get-BootCrc32 -Data $image) -bxor [uint32]::MaxValue

$record = New-Object byte[] 32
$fields = @(
  [uint32]0x424F4F54,          # magic 'BOOT'
  [uint32]2,                   # version
  [uint32]$Seq,                # seq
  [uint32]0,                   # state: BOOT_STATE_IDLE
  [uint32]0,                   # slot: always 0
  [uint32]$image.Length,       # image_size
  [uint32]$imageCrc            # image_crc
)
for ($i = 0; $i -lt $fields.Count; $i++) {
  [Array]::Copy([BitConverter]::GetBytes($fields[$i]), 0, $record, $i * 4, 4)
}
$flagCrc = Get-BootCrc32 -Data $record[0..27]      # no final xor here
[Array]::Copy([BitConverter]::GetBytes([uint32]$flagCrc), 0, $record, 28, 4)

if (-not $Out) {
  $Out = Join-Path (Split-Path -Parent (Resolve-Path $Bin).Path) 'flag.bin'
}
[System.IO.File]::WriteAllBytes($Out, $record)

Write-Host ("image    : {0} ({1} bytes, crc32 0x{2:X8})" -f (Split-Path -Leaf $Bin), $image.Length, $imageCrc)
Write-Host ("flag     : {0} (32 bytes, seq {1}, record crc 0x{2:X8})" -f $Out, $Seq, $flagCrc)
Write-Host 'usage    : tools\flash.cmd -Image app -Via openocd -FlagRecord <flag.bin>'
