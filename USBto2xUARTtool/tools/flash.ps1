<#
.SYNOPSIS
  Flash the USBto2xUARTtool firmware onto an STM32F103 through the DAPLink probe
  (drag-and-drop mass storage) or through OpenOCD/CMSIS-DAP.

.DESCRIPTION
  Images are taken from ..\dist (the published artefacts, newest file wins).

  -Via daplink  copies a .bin to the DAPLINK drive, which programmes it at
                0x08000000 and resets the target.  Because the mass storage
                interface always writes at 0x08000000, "app" is turned into a
                combined image (bootloader + 0xFF padding + application) so one
                drop updates both.  No extra software is needed.

  -Via openocd  talks to the probe through OpenOCD over SWD.  The STM32F103 is
                programmed at the exact address of the requested region and the
                result is verified.  The DAPLink on this machine only answers on
                the CMSIS-DAP HID backend (the WinUSB bulk backend reports
                "unable to find a matching CMSIS-DAP device"), so the config uses
                HID and works with both the 0.11 and the 0.12 builds.

.EXAMPLE
  .\flash.ps1 -Image combined            # drag-and-drop, bootloader + app
.EXAMPLE
  .\flash.ps1 -Image bootloader -Via openocd
.EXAMPLE
  .\flash.ps1 -Erase -Via openocd        # mass erase, then flash nothing
#>
[CmdletBinding()]
param(
  [ValidateSet('combined', 'bootloader', 'app')]
  [string]$Image = 'combined',

  [ValidateSet('daplink', 'openocd')]
  [string]$Via = 'daplink',

  [switch]$Erase,

  # Optional 32-byte flag record (tools\bootflag.ps1 output) written to the flag
  # page after the image.  Only usable with -Via openocd; it is a bring-up aid
  # that lets the bootloader start an image without a YModem download.
  [string]$FlagRecord = '',

  [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

$toolsDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$root = Split-Path -Parent $toolsDir
$dist = Join-Path $root 'dist'

$BOOT_ADDR = 0x08000000
$APP_ADDR = 0x08005C00
$FLASH_END = 0x08010000
$BOOT_MAX = $APP_ADDR - $BOOT_ADDR      # 23 552 bytes (22 KB + 1 KB flag page)
$APP_MAX = $FLASH_END - $APP_ADDR       # 41 984 bytes (41 KB)

function Get-PublishedImage {
  param([string]$Pattern, [string]$What)

  $files = @(Get-ChildItem -Path $dist -Filter $Pattern -File -ErrorAction SilentlyContinue |
             Sort-Object LastWriteTime)
  if ($files.Count -eq 0) {
    throw "no $What image matching '$Pattern' in $dist - build the project first (make -j4)"
  }
  return $files[-1]
}

function Get-Crc32 {
  param([byte[]]$Data)

  # Same variant as Boot_Crc32() in F103-Bootloader/Core/Src/boot.c:
  # MSB-first, poly 0x04C11DB7, init 0xFFFFFFFF, final xor.  This is NOT the
  # common reflected 0xEDB88320 CRC-32 that zlib and friends use, so the value
  # printed here matches the one ymodem-ota reports.
  $allOnes = [uint32]::MaxValue
  $poly = [uint32]0x04C11DB7
  # 0x80000000 as a literal is -2147483648 to PowerShell 5.1, so spell it out.
  $highBit = [uint32]2147483648

  if ($null -eq $script:crcTable) {
    $script:crcTable = New-Object uint32[] 256
    for ($i = 0; $i -lt 256; $i++) {
      $c = [uint32]([uint32]$i -shl 24)
      for ($b = 0; $b -lt 8; $b++) {
        if ($c -band $highBit) {
          $c = [uint32](([uint32]($c -shl 1)) -bxor $poly)
        } else {
          $c = [uint32]($c -shl 1)
        }
      }
      $script:crcTable[$i] = $c
    }
  }

  $crc = $allOnes
  foreach ($byte in $Data) {
    $index = ((($crc -shr 24) -bxor $byte) -band 0xFF)
    $crc = [uint32](([uint32]($crc -shl 8)) -bxor $script:crcTable[$index])
  }
  return [uint32]($crc -bxor $allOnes)
}

function New-CombinedImage {
  param([string]$BootPath, [string]$AppPath)

  $boot = [System.IO.File]::ReadAllBytes($BootPath)
  $app = [System.IO.File]::ReadAllBytes($AppPath)

  if ($boot.Length -gt $BOOT_MAX) {
    throw ("bootloader is {0} bytes but only {1} fit below 0x{2:X8}" -f $boot.Length, $BOOT_MAX, $APP_ADDR)
  }
  if ($app.Length -gt $APP_MAX) {
    throw ("application is {0} bytes but only {1} fit in the slot" -f $app.Length, $APP_MAX)
  }

  $size = ($APP_ADDR - $BOOT_ADDR) + $app.Length
  $image = New-Object byte[] $size
  for ($i = 0; $i -lt $size; $i++) { $image[$i] = 0xFF }
  [Array]::Copy($boot, 0, $image, 0, $boot.Length)
  [Array]::Copy($app, 0, $image, ($APP_ADDR - $BOOT_ADDR), $app.Length)

  $stamp = Get-Date -Format 'yyyyMMdd-HHmm'
  $path = Join-Path $dist "combined-$stamp.bin"
  [System.IO.File]::WriteAllBytes($path, $image)
  return $path
}

function Find-DaplinkDrive {
  $drives = @(Get-PSDrive -PSProvider FileSystem -ErrorAction SilentlyContinue)
  foreach ($drive in $drives) {
    $rootPath = $drive.Root
    if (Test-Path (Join-Path $rootPath 'DETAILS.TXT')) { return $rootPath }
  }
  return $null
}

function Invoke-DaplinkFlash {
  param([string]$Path)

  $drive = Find-DaplinkDrive
  if (-not $drive) { throw "no DAPLINK drive found - is the probe plugged in and showing MBED.HTM?" }

  $name = Split-Path -Leaf $Path
  $target = Join-Path $drive $name
  Write-Host "probe    : $drive ($name)"
  Write-Host "image    : $Path"

  if ($DryRun) { Write-Host 'dry run  : nothing was copied'; return }

  $fail = Join-Path $drive 'FAIL.TXT'
  if (Test-Path $fail) { Remove-Item $fail -Force -ErrorAction SilentlyContinue }

  Copy-Item -LiteralPath $Path -Destination $target -Force
  Write-Host 'copied   : waiting for the probe to program and reset the target ...'

  # The probe deletes the file and remounts once programming is finished.
  $deadline = (Get-Date).AddSeconds(60)
  while ((Get-Date) -lt $deadline) {
    Start-Sleep -Milliseconds 500
    $stillThere = Test-Path $target
    $driveNow = Find-DaplinkDrive
    if (-not $stillThere -and $driveNow) { break }
  }

  $drive = Find-DaplinkDrive
  if (-not $drive) { throw 'the probe did not come back after programming - replug it' }
  $fail = Join-Path $drive 'FAIL.TXT'
  if (Test-Path $fail) {
    $text = Get-Content $fail -Raw
    Remove-Item $fail -Force -ErrorAction SilentlyContinue
    throw "programming failed: $text"
  }
  Write-Host 'done     : image programmed, target reset'
}

function Get-OpenOcdVersion {
  param([string]$Exe, [string]$Text)

  if ($Text -match '(\d+)\.(\d+)\.(\d+)') {
    return [version]("{0}.{1}.{2}" -f $matches[1], $matches[2], $matches[3])
  }
  return [version]'0.0.0'
}

function Find-OpenOcd {
  # Collect every OpenOCD on this machine and keep the newest one: the 0.11
  # build that ships with the sysprogs package cannot drive a CMSIS-DAP v2
  # (WebUSB) probe, while the 0.12 build bundled with the Espressif tools can.
  $candidates = New-Object System.Collections.Generic.List[string]

  $cmd = Get-Command openocd -ErrorAction SilentlyContinue
  if ($cmd) { $candidates.Add($cmd.Source) }

  foreach ($root in @('D:\EmbeddedDevelopTools', 'C:\Program Files', 'C:\Program Files (x86)')) {
    if (Test-Path $root) {
      Get-ChildItem -Path $root -Recurse -Filter 'openocd.exe' -File -ErrorAction SilentlyContinue |
        ForEach-Object { $candidates.Add($_.FullName) }
    }
  }

  if ($candidates.Count -eq 0) {
    throw 'openocd.exe not found - put it on PATH or drop it under D:\EmbeddedDevelopTools'
  }

  $savedPolicy = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  $best = $null
  $bestVersion = [version]'0.0.0'
  foreach ($exe in ($candidates | Select-Object -Unique)) {
    $text = (& $exe --version 2>&1 | Out-String)
    $ver = Get-OpenOcdVersion -Exe $exe -Text $text
    Write-Verbose "found $exe ($ver)"
    if ($ver -gt $bestVersion) { $bestVersion = $ver; $best = $exe }
  }
  $ErrorActionPreference = $savedPolicy

  if (-not $best) { return $candidates[0] }
  Write-Host ("openocd  : {0} (v{1})" -f $best, $bestVersion)
  return $best
}

function Invoke-OpenOcdFlash {
  param([string]$Path, [uint32]$Address, [switch]$EraseOnly, [string]$FlagPath = '')

  $exe = Find-OpenOcd
  # OpenOCD runs with tools\ as its working directory, so every path handed to it
  # must be absolute.
  if ($FlagPath) { $FlagPath = (Resolve-Path $FlagPath).Path }
  $Path = (Resolve-Path $Path).Path
  # openocd --version writes to stderr and exits non-zero, which would turn into
  # a terminating NativeCommandError under $ErrorActionPreference = 'Stop'.
  $savedPolicy = $ErrorActionPreference
  $ErrorActionPreference = 'Continue'
  $version = (& $exe --version 2>&1 | Out-String)
  $ErrorActionPreference = $savedPolicy
  $ver = Get-OpenOcdVersion -Exe $exe -Text $version

  $lines = New-Object System.Collections.Generic.List[string]
  $lines.Add('source [find interface/cmsis-dap.cfg]')
  # The probe only answers on the HID backend (verified: bulk -> "unable to find
  # a matching CMSIS-DAP device", HID -> DPIDR 0x1ba01477).  0.12 renamed the
  # command, so spell it for the version in use.
  if ($ver -ge [version]'0.12.0') { $lines.Add('cmsis-dap backend hid') }
  else { $lines.Add('cmsis_dap_backend hid') }
  $lines.Add('transport select swd')
  $lines.Add('adapter speed 1000')
  $lines.Add('reset_config none')
  $lines.Add('gdb_port 0')
  $lines.Add('telnet_port 0')
  $lines.Add('source [find target/stm32f1x.cfg]')
  $lines.Add('init')
  if ($EraseOnly) {
    $lines.Add('reset halt')
    $lines.Add('stm32f1x mass_erase 0')
    $lines.Add('reset run')
  } else {
    $lines.Add('reset halt')
    # 'program' erases the touched pages, writes and verifies them.
    $lines.Add(('program "{0}" 0x{1:X8} verify' -f ($Path -replace '\\', '/'), $Address))
    if ($FlagPath) {
      # The flag record goes in last: a failed image must not leave a record
      # that claims a valid application.
      $lines.Add(('flash write_image erase "{0}" 0x{1:X8}' -f ($FlagPath -replace '\\', '/'), 0x08005800))
      $lines.Add(('verify_image "{0}" 0x{1:X8}' -f ($FlagPath -replace '\\', '/'), 0x08005800))
    }
    $lines.Add('reset run')
  }
  $lines.Add('shutdown')

  $cfg = Join-Path $toolsDir 'openocd\_run.cfg'
  New-Item -ItemType Directory -Force -Path (Split-Path $cfg) | Out-Null
  [System.IO.File]::WriteAllLines($cfg, $lines, (New-Object System.Text.ASCIIEncoding))

  if (-not $EraseOnly) { Write-Host "image    : $Path @ 0x$($Address.ToString('X8'))" }
  if ($FlagPath) { Write-Host "flagrec  : $FlagPath @ 0x08005800" }
  if ($DryRun) { Write-Host "dry run  : openocd was not started, config is $cfg"; return }

  $out = Join-Path $toolsDir 'openocd\_run.out.txt'
  $err = Join-Path $toolsDir 'openocd\_run.err.txt'
  Remove-Item $out, $err -ErrorAction SilentlyContinue
  $proc = Start-Process -FilePath $exe `
    -ArgumentList '-f', 'openocd/_run.cfg' `
    -WorkingDirectory $toolsDir -PassThru -WindowStyle Hidden `
    -RedirectStandardOutput $out -RedirectStandardError $err
  if (-not $proc.WaitForExit(90000)) {
    $proc.Kill()
    throw 'openocd did not finish in 90 s - check the probe and the target supply'
  }

  $log = @()
  if (Test-Path $err) { $log += Get-Content $err }
  if (Test-Path $out) { $log += Get-Content $out }
  $log | Where-Object { $_ -match 'Error|Warn|programmed|Verified|erased|wrote' } | ForEach-Object { Write-Host "  $_" }

  $errors = @($log | Where-Object { $_ -match '^\s*Error' })
  if ($errors.Count -gt 0) { throw ($errors -join [Environment]::NewLine) }
  Write-Host 'done     : openocd finished'
}

# ------------------------------------------------------------------ main flow

Write-Host 'USBto2xUARTtool flasher'
Write-Host ("layout   : bootloader 0x{0:X8} ({1} B max), application 0x{2:X8} ({3} B max)" -f `
  $BOOT_ADDR, $BOOT_MAX, $APP_ADDR, $APP_MAX)

$flashPath = $null
$flashAddress = $BOOT_ADDR

if (-not $Erase) {
  $bootFile = Get-PublishedImage -Pattern 'bootloader-*.bin' -What 'bootloader'
  $appFile = Get-PublishedImage -Pattern 'app-*.bin' -What 'application'

  switch ($Image) {
    'bootloader' {
      $flashPath = $bootFile.FullName
      $flashAddress = $BOOT_ADDR
      if ($Via -eq 'daplink') {
        Write-Host 'note     : mass storage always writes at 0x08000000, sending the bootloader alone'
        Write-Host 'note     : a combined image is safer, use -Image combined'
      }
    }
    'app' {
      if ($Via -eq 'daplink') {
        $flashPath = New-CombinedImage -BootPath $bootFile.FullName -AppPath $appFile.FullName
        Write-Host 'note     : mass storage cannot write at 0x08005C00, a combined image was built'
      } else {
        $flashPath = $appFile.FullName
        $flashAddress = $APP_ADDR
      }
    }
    'combined' {
      $flashPath = New-CombinedImage -BootPath $bootFile.FullName -AppPath $appFile.FullName
      $flashAddress = $BOOT_ADDR
    }
  }

  $bytes = [System.IO.File]::ReadAllBytes($flashPath)
  $crc = Get-Crc32 -Data $bytes
  Write-Host ("image    : {0} ({1} bytes, crc32 0x{2:X8})" -f (Split-Path -Leaf $flashPath), $bytes.Length, $crc)
  Write-Host 'note     : ymodem-ota prints its own crc32, compare it with the value above'
}

switch ($Via) {
  'daplink' {
    if ($Erase) {
      throw 'the mass storage interface cannot mass erase - use -Via openocd -Erase, or flash -Image combined'
    }
    Invoke-DaplinkFlash -Path $flashPath
  }
  'openocd' {
    if ($Erase) { Invoke-OpenOcdFlash -EraseOnly }
    elseif ($FlagRecord) { Invoke-OpenOcdFlash -Path $flashPath -Address $flashAddress -FlagPath $FlagRecord }
    else { Invoke-OpenOcdFlash -Path $flashPath -Address $flashAddress }
  }
}
