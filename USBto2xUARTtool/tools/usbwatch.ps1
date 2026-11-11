param(
  [int]$Seconds = 120,
  [string]$Out = 'logs\usb-watch.log'
)
$ErrorActionPreference = 'Continue'
$path = Join-Path (Split-Path -Parent $PSScriptRoot) $Out
$lines = New-Object System.Collections.Generic.List[string]
$last = ''
$deadline = (Get-Date).AddSeconds($Seconds)
while ((Get-Date) -lt $deadline) {
  $ids = @()
  foreach ($l in (pnputil /enum-devices /connected 2>$null)) {
    $t = $l.Trim()
    if ($t -match '^Instance ID:\s+(USB\\.+)$') { $ids += $Matches[1] }
  }
  $state = ($ids | Sort-Object) -join ';'
  if ($state -ne $last) {
    $stamp = (Get-Date).ToString('HH:mm:ss.fff')
    $new = @()
    foreach ($i in $ids) { if ($last -notlike "*$i*") { $new += $i } }
    $gone = @()
    if ($last) { foreach ($i in ($last -split ';')) { if ($i -and ($ids -notcontains $i)) { $gone += $i } } }
    if (-not $last) {
      $lines.Add("$stamp initial: " + ($ids.Count) + " usb devices")
    } else {
      foreach ($i in $new)  { $lines.Add("$stamp + $i") }
      foreach ($i in $gone) { $lines.Add("$stamp - $i") }
    }
    [System.IO.File]::WriteAllText($path, ($lines -join "`r`n") + "`r`n", (New-Object System.Text.UTF8Encoding($false)))
    $last = $state
  }
  Start-Sleep -Milliseconds 300
}
Write-Output "watch finished, $($lines.Count) events -> $path"
