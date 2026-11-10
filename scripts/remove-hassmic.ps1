<#
.SYNOPSIS
    Removes what hassmic left on an Echo Dot 2nd gen (biscuit) or Echo 2nd gen (radar) that now runs
    EchoLocal.

.DESCRIPTION
    hassmic (a Home Assistant satellite for Echos) installs onto the system partition and data, and its
    lockdown firewall closes EchoLocal's API port every few seconds. This removes what hassmic's own
    uninstaller (sysinstall.sh uninstall) would, plus what its runtime leaves in /data:

      /data/local/hassmic/                    config (hassmic.conf), state, logs, wake word files
      /data/misc/avahi/services/hassmic.service, /data/misc/avahi/avahi-daemon.conf
                                              its mDNS advertisement and the copy of avahi's config it wrote
      /system/hassmic/, /system/etc/init/hassmic.rc
      /system/etc/led-resources/hassmic_*.animation, /system/etc/hassmic-leds.digest
      /system/etc/led-resources/layer_config_common.json, put back from stock's .pre-hassmic copy

    It keeps:
      /sepolicy                 On both Dots, hassmic replaces this file and EchoLocal's installer then
                                patches it (init permissive). Putting back /sepolicy.pre-hassmic would
                                drop that patch, which echod relies on.
      persist.sys.timezone, Wi-Fi networks, and everything EchoLocal installed.

    Nothing is written until every check passes and you confirm. Run with -WhatIf to see the plan only.

.PARAMETER Serial
    The adb serial, when more than one device is attached.

.PARAMETER Yes
    Do not ask before removing.

.PARAMETER Reboot
    Reboot at the end and check the device comes back clean, without asking.

.EXAMPLE
    .\scripts\remove-hassmic.ps1 -WhatIf
.EXAMPLE
    .\scripts\remove-hassmic.ps1 -Serial G090LF0000000000
#>
[CmdletBinding(SupportsShouldProcess)]
param(
    [string]$Serial,
    [switch]$Yes,
    [switch]$Reboot
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$SystemLabel = 'u:object_r:system_file:s0'
$LedDir = '/system/etc/led-resources'
$LedConfig = "$LedDir/layer_config_common.json"
$LedStock = '/system/etc/layer_config_common.json.pre-hassmic'

# The Dots both hassmic and EchoLocal install onto: one platform, /sepolicy on the system partition,
# ledcontroller taken over. The Shows keep their policy elsewhere and have stock apps moved off.
$Supported = @{ 'biscuit_puffin' = 'Echo Dot 2nd gen (biscuit)'; 'radar_puffin' = 'Echo 2nd gen (radar)' }

if (-not (Get-Command adb -ErrorAction SilentlyContinue)) { throw 'adb is not on PATH' }
$adbArgs = if ($Serial) { @('-s', $Serial) } else { @() }

# Runs one command on the device and returns its output; throws unless it exits 0. The exit status is
# echoed as a marker because adb shell does not pass it back from every adbd these devices run.
function Invoke-Device([string]$command, [switch]$AllowFailure) {
    $out = & adb @adbArgs shell ($command + '; echo __rc=$?') 2>&1 | ForEach-Object { "$_" }
    $text = ($out -join "`n").TrimEnd()
    if ($text -notmatch '(?s)^(.*?)\n?__rc=(\d+)$') { throw "adb: no answer to: $command`n$text" }
    $body, $rc = $Matches[1].TrimEnd(), [int]$Matches[2]
    if ($rc -ne 0 -and -not $AllowFailure) { throw "device: '$command' exited $rc`n$body" }
    return $body
}

function Test-Device([string]$test) {
    (Invoke-Device "if $test; then echo yes; else echo no; fi") -eq 'yes'
}

Write-Host 'Checking the device'
$devices = @(& adb devices | Select-Object -Skip 1 | Where-Object { $_ -match '\tdevice$' })
if ($devices.Count -eq 0) { throw 'no device attached: connect the Echo over USB and check adb devices' }
if (-not $Serial -and $devices.Count -gt 1) {
    throw "$($devices.Count) devices attached; pass -Serial with one of them (adb devices lists them)"
}
$product = Invoke-Device 'getprop ro.product.device'
if (-not $Supported.ContainsKey($product)) {
    throw "this is $product; this script knows only $(($Supported.Keys | Sort-Object) -join ' and ')"
}
$model = $Supported[$product]
if ((Invoke-Device 'id -u') -ne '0') { throw 'adb is not running as root on the device' }

# Only after EchoLocal: on a device still running Amazon's software, hassmic's lockdown is what keeps
# Amazon's updaters off the internet, and EchoLocal is what disables them once it is installed.
$service = Invoke-Device 'readlink /system/bin/ledcontroller' -AllowFailure
if ($service -ne '/system/app/echod/echod') {
    throw 'EchoLocal is not installed here (ledcontroller is not echod); this script is for after it is'
}
# hassmic's own uninstall puts these back where they came from; deleting /system/hassmic without
# doing that would leave stock apps lost in a folder nothing scans. Only the Shows ever get one.
if (Test-Device '[ -e /system/hassmic-removed ]') {
    throw '/system/hassmic-removed exists: hassmic moved stock apps off the partition, and this script does not put them back'
}

# What is there to do, each a description and the device commands that do it. Only what is present
# goes in, so a second run finds nothing. Order matters: /data/local/hassmic goes first, because
# without hassmic.conf its boot.sh does nothing, so a run cut short still leaves hassmic switched off.
$plan = [System.Collections.Generic.List[object]]::new()
function Add-Step([string]$what, [string]$command, [bool]$system) {
    $plan.Add([pscustomobject]@{ What = $what; Command = $command; System = $system })
}

# A running hassmic_fw re-applies its lockdown every few seconds, and the satellite rewrites its files.
foreach ($svc in 'hassmic', 'hassmic_fw') {
    if ((Invoke-Device "getprop init.svc.$svc") -eq 'running') {
        Add-Step "stop the $svc service" "setprop ctl.stop $svc" $false
    }
}
if (Test-Device '[ -e /data/local/hassmic ]') {
    $size = Invoke-Device 'du -sk /data/local/hassmic | cut -f1'
    Add-Step "remove /data/local/hassmic ($size kB, including hassmic.conf)" 'rm -rf /data/local/hassmic' $false
}
# Both written by hassmic: its ESPHome advertisement (port 26053), and a copy of stock's
# /system/etc/avahi-daemon.conf with its node name added. Stock's file and the folder stay.
foreach ($file in '/data/misc/avahi/services/hassmic.service', '/data/misc/avahi/avahi-daemon.conf') {
    if (Test-Device "[ -e $file ]") { Add-Step "remove $file" "rm -f $file" $false }
}

# The ledcontroller that read these is gone, but the partition goes back to what stock shipped. Only
# while the config still names hassmic's animations, as hassmic's own check does. Staged as .new and
# renamed over, so a cut leaves the old file or the new one, never half of one; system_file is the
# label everything in /system/etc carries.
if ((Test-Device "[ -f $LedStock ]") -and (Test-Device "grep -q hassmic_ $LedConfig")) {
    Add-Step "put stock's $LedConfig back" ("cp $LedStock $LedConfig.new && chmod 644 $LedConfig.new && " +
        "chcon $SystemLabel $LedConfig.new && mv $LedConfig.new $LedConfig && rm $LedStock") $true
}
# About 2,400 files: find hands them to rm in batches, where a shell glob would build one huge command.
$animations = [int](Invoke-Device "ls $LedDir | grep -c '^hassmic_.*\.animation$'" -AllowFailure)
if ($animations -gt 0) {
    Add-Step "remove $animations hassmic_*.animation files" `
        "find $LedDir -name 'hassmic_*.animation' -exec rm -f {} +" $true
}
# HassmicHome is the Shows' home screen; it is listed so an unexpected one is not left behind.
foreach ($path in '/system/etc/hassmic-leds.digest', '/system/etc/init/hassmic.rc', '/system/app/HassmicHome') {
    if (Test-Device "[ -e $path ]") { Add-Step "remove $path" "rm -rf $path" $true }
}
if (Test-Device '[ -e /system/hassmic ]') {
    Add-Step 'remove /system/hassmic' 'rm -rf /system/hassmic' $true
}

if ($plan.Count -eq 0) {
    Write-Host 'Nothing of hassmic is left on this device.'
    return
}

Write-Host "`nOn this $model, this will:"
$plan | ForEach-Object { Write-Host "  - $($_.What)" }
Write-Host '  and keep /sepolicy, the time zone, Wi-Fi, and everything EchoLocal installed.'

if ($WhatIfPreference) { return }
if (-not $Yes) {
    $answer = Read-Host "`nGo ahead? [y/N]"
    if ($answer -notmatch '^(y|yes)$') { Write-Host 'Nothing changed.'; return }
}

$systemSteps = @($plan | Where-Object System)
if ($systemSteps.Count -gt 0) {
    Write-Host 'making /system writable'
    & adb @adbArgs remount | Out-Null
    # adb remount's output is not something to parse; writing a test file shows it is writable.
    Invoke-Device 'touch /system/.hassmic-rw-test && rm /system/.hassmic-rw-test' | Out-Null
}

try {
    foreach ($step in $plan) {
        Write-Host "  $($step.What)"
        Invoke-Device $step.Command | Out-Null
    }
    Invoke-Device 'sync' | Out-Null
}
finally {
    # A reboot mounts it read-only anyway; this covers stopping without one. These are system-as-root
    # devices, where /system is the root filesystem, hence / first.
    if ($systemSteps.Count -gt 0) {
        Invoke-Device 'mount -o remount,ro / 2>/dev/null || mount -o remount,ro /system' -AllowFailure | Out-Null
    }
}
Write-Host 'Done.'

# hassmic's firewall rules are in memory until the next boot builds the stock ones again. -Yes alone
# never reboots: an unattended run should not take a device down that nobody asked to restart.
if (-not $Reboot) {
    if ($Yes) { Write-Host 'Reboot to drop the firewall rules hassmic left in memory.'; return }
    if ((Read-Host 'Reboot now to clear the firewall rules hassmic left in memory? [Y/n]') -match '^(n|no)$') { return }
}

Write-Host 'rebooting'
& adb @adbArgs reboot
& adb @adbArgs wait-for-device
# adb comes back well before echod does; resident is when it has finished starting.
$deadline = (Get-Date).AddMinutes(3)
do {
    Start-Sleep -Seconds 3
    $state = Invoke-Device 'getprop echolocal.state' -AllowFailure
} until ($state -eq 'resident' -or (Get-Date) -gt $deadline)

# ls prints only what still exists, and the count of hassmic_out rules ends the output: a lone 0 is clean.
$left = Invoke-Device ('ls -d /system/hassmic /system/etc/init/hassmic.rc /data/local/hassmic ' +
    '/data/misc/avahi/services/hassmic.service 2>/dev/null; iptables -S 2>/dev/null | grep -c hassmic_out') -AllowFailure
Write-Host "echod: $state"
if ($left.Trim() -eq '0') { Write-Host 'hassmic: nothing left, and no hassmic firewall rules' }
else { Write-Warning "still there:`n$left" }
