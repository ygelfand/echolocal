#requires -Version 5.1

<#
.SYNOPSIS
Build and provision EchoLocal with local Amazon Pryon/Alexa wake detection.

.DESCRIPTION
This is the supported source-tree installer for an unlocked Echo Dot 2 with TWRP recovery. It
selects only an Amazon "biscuit" device, proves SDK 22, builds every EchoLocal-owned payload, saves a
rollback snapshot, installs the verified root/permissive boot image when needed, installs and reboots
the Dot, verifies the ESPHome native API and Pryon, then prints the encryption key last.

Amazon libraries, models, and SpeechInteractionManager remain on the user's own Dot and are
discovered there. They are never copied into the source tree or embedded in the installer.

.EXAMPLE
.\provision-echo-dot.ps1 -Name "Kitchen Echo"

.EXAMPLE
.\provision-echo-dot.ps1 -Serial G090XXXXXXXXXXXX -Name "Kitchen Echo"
#>
[CmdletBinding()]
param(
    [string]$Serial,
    [string]$Name,
    [string]$SdkRoot,
    [string]$BuildToolsVersion,
    [string]$PlatformVersion,
    [string]$KeyStore,
    [switch]$SkipBuild,
    [switch]$BuildOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
if (Test-Path Variable:\PSNativeCommandUseErrorActionPreference) {
    $PSNativeCommandUseErrorActionPreference = $false
}

$projectDir = [IO.Path]::GetFullPath($PSScriptRoot)
$binDir = Join-Path $projectDir "bin"
$payloadDir = Join-Path $projectDir "internal\host\assets\payload"
$echoctlPath = Join-Path $binDir "echoctl-provision.exe"
$echodPath = Join-Path $binDir "echod-provision"

function Write-Section {
    param([Parameter(Mandatory = $true)][string]$Text)
    Write-Host ""
    Write-Host "== $Text ==" -ForegroundColor Cyan
}

function Resolve-Executable {
    param(
        [Parameter(Mandatory = $true)][string]$Name,
        [string[]]$Candidates = @()
    )

    foreach ($candidate in $Candidates) {
        if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }

    $command = Get-Command $Name -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $command) {
        throw "Required program '$Name' was not found."
    }
    return $command.Source
}

function Invoke-Program {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [string]$Description = $FilePath
    )

    & $FilePath @ArgumentList
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE."
    }
}

function Get-ProgramOutput {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [string]$Description = $FilePath
    )

    $lines = @(& $FilePath @ArgumentList 2>&1)
    if ($LASTEXITCODE -ne 0) {
        $detail = ($lines | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine
        throw "$Description failed with exit code $LASTEXITCODE.`n$detail"
    }
    return (($lines | ForEach-Object { $_.ToString() }) -join [Environment]::NewLine).Trim()
}

function Find-AndroidSdk {
    if ($SdkRoot) {
        return [IO.Path]::GetFullPath($SdkRoot)
    }
    foreach ($candidate in @(
        $env:ANDROID_SDK_ROOT,
        $env:ANDROID_HOME,
        (Join-Path $env:LOCALAPPDATA "Android\Sdk")
    )) {
        if ($candidate -and (Test-Path -LiteralPath $candidate -PathType Container)) {
            return [IO.Path]::GetFullPath($candidate)
        }
    }
    throw "Android SDK not found. Pass -SdkRoot or set ANDROID_SDK_ROOT."
}

function Find-BuildToolsVersion {
    param([Parameter(Mandatory = $true)][string]$AndroidSdk)
    if ($BuildToolsVersion) {
        return $BuildToolsVersion
    }

    $root = Join-Path $AndroidSdk "build-tools"
    $versions = @(Get-ChildItem -LiteralPath $root -Directory -ErrorAction SilentlyContinue |
        Where-Object {
            (Test-Path -LiteralPath (Join-Path $_.FullName "aapt.exe") -PathType Leaf) -and
            (Test-Path -LiteralPath (Join-Path $_.FullName "d8.bat") -PathType Leaf) -and
            (Test-Path -LiteralPath (Join-Path $_.FullName "zipalign.exe") -PathType Leaf) -and
            (Test-Path -LiteralPath (Join-Path $_.FullName "apksigner.bat") -PathType Leaf)
        } | Sort-Object {
            try { [version]$_.Name } catch { [version]"0.0" }
        } -Descending)
    if ($versions.Count -eq 0) {
        throw "No complete Android build-tools installation was found under $root."
    }
    return $versions[0].Name
}

function Find-PlatformVersion {
    param([Parameter(Mandatory = $true)][string]$AndroidSdk)
    if ($PlatformVersion) {
        return $PlatformVersion
    }

    $root = Join-Path $AndroidSdk "platforms"
    $platforms = @(Get-ChildItem -LiteralPath $root -Directory -ErrorAction SilentlyContinue |
        Where-Object {
            $_.Name -match '^android-(\d+)$' -and
            (Test-Path -LiteralPath (Join-Path $_.FullName "android.jar") -PathType Leaf)
        } | Sort-Object { [int]($_.Name.Substring("android-".Length)) } -Descending)
    if ($platforms.Count -eq 0) {
        throw "No Android platform with android.jar was found under $root."
    }
    return $platforms[0].Name
}

function Ensure-DebugKeyStore {
    param([Parameter(Mandatory = $true)][string]$Path)
    if (Test-Path -LiteralPath $Path -PathType Leaf) {
        return
    }

    Write-Host "Creating Android debug signing key: $Path"
    $keytool = Resolve-Executable -Name "keytool.exe"
    $parent = Split-Path -Parent $Path
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
    Invoke-Program -FilePath $keytool -Description "keytool" -ArgumentList @(
        "-genkeypair", "-keystore", $Path,
        "-storepass", "android", "-alias", "androiddebugkey", "-keypass", "android",
        "-dname", "CN=Android Debug,O=Android,C=US",
        "-keyalg", "RSA", "-keysize", "2048", "-validity", "10000"
    )
}

function Build-Installer {
    Write-Section "Checking build tools"
    $go = Resolve-Executable -Name "go.exe"
    [void](Resolve-Executable -Name "javac.exe")
    [void](Resolve-Executable -Name "jar.exe")

    $androidSdk = Find-AndroidSdk
    $toolsVersion = Find-BuildToolsVersion -AndroidSdk $androidSdk
    $platform = Find-PlatformVersion -AndroidSdk $androidSdk
    if (-not $KeyStore) {
        $script:KeyStore = Join-Path $env:USERPROFILE ".android\debug.keystore"
    }
    $resolvedKeyStore = [IO.Path]::GetFullPath($KeyStore)
    Ensure-DebugKeyStore -Path $resolvedKeyStore

    Write-Host "Android SDK:       $androidSdk"
    Write-Host "Build tools:       $toolsVersion"
    Write-Host "Android platform:  $platform"

    Write-Section "Building EchoLocal Pryon companion"
    & (Join-Path $projectDir "android\pryon\build.ps1") `
        -SdkRoot $androidSdk `
        -BuildToolsVersion $toolsVersion `
        -PlatformVersion $platform `
        -KeyStore $resolvedKeyStore

    Write-Section "Building Android media bridge"
    & (Join-Path $projectDir "android\amazon-helper\build.ps1") `
        -SdkRoot $androidSdk `
        -BuildToolsVersion $toolsVersion `
        -PlatformVersion $platform

    New-Item -ItemType Directory -Force -Path $binDir, $payloadDir | Out-Null

    Write-Section "Building EchoLocal device agent"
    $savedGoos = $env:GOOS
    $savedGoarch = $env:GOARCH
    $savedCgo = $env:CGO_ENABLED
    try {
        $env:GOOS = "linux"
        $env:GOARCH = "arm64"
        $env:CGO_ENABLED = "0"
        Invoke-Program -FilePath $go -Description "echod build" -ArgumentList @(
            "build", "-trimpath", "-ldflags", "-s -w", "-o", $echodPath, ".\cmd\echod"
        )
    } finally {
        $env:GOOS = $savedGoos
        $env:GOARCH = $savedGoarch
        $env:CGO_ENABLED = $savedCgo
    }

    Write-Section "Embedding the complete installer payload"
    $payloads = @{
        "echod" = $echodPath
        "boot.img" = (Join-Path $projectDir "images\echolocal-boot.img")
        "EchoLocalPryon.apk" = (Join-Path $projectDir "android\pryon\build\EchoLocalPryon.apk")
        "amazon-helper.jar" = (Join-Path $projectDir "android\amazon-helper\build\amazon-helper.jar")
    }
    foreach ($item in $payloads.GetEnumerator()) {
        if (-not (Test-Path -LiteralPath $item.Value -PathType Leaf)) {
            throw "Required payload is missing: $($item.Value)"
        }
        Copy-Item -Force -LiteralPath $item.Value -Destination (Join-Path $payloadDir $item.Key)
    }

    $savedGoos = $env:GOOS
    $savedGoarch = $env:GOARCH
    $savedCgo = $env:CGO_ENABLED
    try {
        Remove-Item Env:\GOOS -ErrorAction SilentlyContinue
        Remove-Item Env:\GOARCH -ErrorAction SilentlyContinue
        $env:CGO_ENABLED = "0"
        Invoke-Program -FilePath $go -Description "echoctl build" -ArgumentList @(
            "build", "-trimpath", "-tags", "payload", "-o", $echoctlPath, ".\cmd\echoctl"
        )
    } finally {
        $env:GOOS = $savedGoos
        $env:GOARCH = $savedGoarch
        $env:CGO_ENABLED = $savedCgo
    }

    foreach ($path in @(
        $echodPath,
        $echoctlPath,
        (Join-Path $payloadDir "EchoLocalPryon.apk"),
        (Join-Path $payloadDir "amazon-helper.jar")
    )) {
        $file = Get-Item -LiteralPath $path
        $hash = Get-FileHash -Algorithm SHA256 -LiteralPath $path
        Write-Host ("{0}  {1} bytes  sha256={2}" -f $file.Name, $file.Length, $hash.Hash.ToLowerInvariant())
    }
}

function Resolve-Adb {
    $candidates = @()
    if ($SdkRoot) {
        $candidates += (Join-Path $SdkRoot "platform-tools\adb.exe")
    }
    if ($env:ANDROID_SDK_ROOT) {
        $candidates += (Join-Path $env:ANDROID_SDK_ROOT "platform-tools\adb.exe")
    }
    if ($env:ANDROID_HOME) {
        $candidates += (Join-Path $env:ANDROID_HOME "platform-tools\adb.exe")
    }
    $candidates += (Join-Path $env:LOCALAPPDATA "Android\Sdk\platform-tools\adb.exe")
    return Resolve-Executable -Name "adb.exe" -Candidates $candidates
}

function Select-Biscuit {
    param([Parameter(Mandatory = $true)][string]$Adb)

    $listing = Get-ProgramOutput -FilePath $Adb -ArgumentList @("devices", "-l") -Description "adb devices"
    $rows = @()
    foreach ($line in ($listing -split "`r?`n")) {
        if ($line -match '^(?<serial>\S+)\s+(?<state>\S+)(?<details>.*)$' -and
            $Matches.serial -ne "List") {
            $rows += [PSCustomObject]@{
                Serial = $Matches.serial
                State = $Matches.state
                Details = $Matches.details
            }
        }
    }

    if ($Serial) {
        $selected = @($rows | Where-Object { $_.Serial -eq $Serial })
        if ($selected.Count -ne 1) {
            throw "Device '$Serial' is not connected. adb reports:`n$listing"
        }
        if ($selected[0].State -ne "device") {
            throw "Device '$Serial' is $($selected[0].State), not ready."
        }
        if ($selected[0].Details -notmatch '(?:^|\s)device:biscuit(?:\s|$)') {
            throw "Refusing device '$Serial': it is not an Echo Dot 2 (biscuit). Details:$($selected[0].Details)"
        }
        return $Serial
    }

    $biscuits = @($rows | Where-Object {
        $_.State -eq "device" -and $_.Details -match '(?:^|\s)device:biscuit(?:\s|$)'
    })
    if ($biscuits.Count -eq 0) {
        throw "No ready Echo Dot 2 (biscuit) is connected. adb reports:`n$listing"
    }
    if ($biscuits.Count -gt 1) {
        $ids = ($biscuits | ForEach-Object { $_.Serial }) -join ", "
        throw "More than one biscuit is connected ($ids). Re-run with -Serial."
    }
    return $biscuits[0].Serial
}

function Get-AdbShell {
    param(
        [Parameter(Mandatory = $true)][string]$Adb,
        [Parameter(Mandatory = $true)][string]$DeviceSerial,
        [Parameter(Mandatory = $true)][string]$Command
    )
    return Get-ProgramOutput -FilePath $Adb `
        -ArgumentList @("-s", $DeviceSerial, "shell", $Command) `
        -Description "adb shell $Command"
}

function Assert-CompatibleBiscuit {
    param(
        [Parameter(Mandatory = $true)][string]$Adb,
        [Parameter(Mandatory = $true)][string]$DeviceSerial
    )

    $product = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial -Command "getprop ro.product.device"
    $sdk = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial -Command "getprop ro.build.version.sdk"
    $identity = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial -Command "id"
    $selinux = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial -Command "getenforce"
    $booted = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial -Command "getprop sys.boot_completed"

    if ($product -ne "biscuit") {
        throw "Refusing ${DeviceSerial}: ro.product.device is '$product', want 'biscuit'."
    }
    if ($sdk -ne "22") {
        throw "Refusing ${DeviceSerial}: Android SDK is '$sdk', want '22'."
    }
    if ($booted -ne "1") {
        throw "Refusing ${DeviceSerial}: Android has not completed booting."
    }

    Write-Host "Target:  $DeviceSerial"
    Write-Host "Device:  $product / SDK $sdk"
    if ($identity -match 'uid=0\(root\)' -and $selinux -eq "Permissive") {
        Write-Host "Boot:    already root and SELinux Permissive"
    } else {
        Write-Host "Boot:    stock runtime ($identity; SELinux $selinux)"
        Write-Host "         echoctl will verify and install its boot image through TWRP recovery."
    }
}

function Save-RollbackSnapshot {
    param(
        [Parameter(Mandatory = $true)][string]$Adb,
        [Parameter(Mandatory = $true)][string]$DeviceSerial
    )

    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $backupDir = Join-Path $projectDir ".local-device-backup\$stamp-$DeviceSerial-before-provision"
    New-Item -ItemType Directory -Force -Path $backupDir | Out-Null

    $inventoryCommand = @'
echo '### identity'
id
getenforce
getprop
echo '### mounts'
mount
echo '### modified-path candidates'
ls -ldZ /system/bin/ledcontroller /system/bin/ledcontroller.orig /system/bin/start_animation.sh /system/bin/start_animation.sh.orig /system/bin/stop_animation.sh /system/bin/stop_animation.sh.orig /system/bin/greengrass_firewall.sh /system/app/echod /system/priv-app/EchoLocalPryon /data/misc/echolocal 2>/dev/null
echo '### visible packages'
pm list packages
echo '### all packages including hidden'
pm list packages -u
'@
    $inventory = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial -Command $inventoryCommand
    Set-Content -LiteralPath (Join-Path $backupDir "device-inventory.txt") -Value $inventory -Encoding UTF8

    $paths = @(
        "/system/bin/ledcontroller",
        "/system/bin/ledcontroller.orig",
        "/system/bin/start_animation.sh",
        "/system/bin/start_animation.sh.orig",
        "/system/bin/stop_animation.sh",
        "/system/bin/stop_animation.sh.orig",
        "/system/bin/greengrass_firewall.sh",
        "/system/app/echod/echod",
        "/system/priv-app/EchoLocalPryon/EchoLocalPryon.apk",
        "/data/misc/echolocal/amazon-helper.jar",
        "/data/misc/echolocal/name",
        "/data/misc/echolocal/psk",
        "/data/misc/echolocal/pryon.uid",
        "/data/misc/echolocal/state.json"
    )
    foreach ($remotePath in $paths) {
        $exists = Get-AdbShell -Adb $Adb -DeviceSerial $DeviceSerial `
            -Command "if [ -e '$remotePath' ]; then echo yes; else echo no; fi"
        if ($exists -ne "yes") {
            continue
        }
        $safeName = $remotePath.TrimStart('/').Replace('/', '__')
        $destination = Join-Path $backupDir $safeName
        Invoke-Program -FilePath $Adb -Description "back up $remotePath" `
            -ArgumentList @("-s", $DeviceSerial, "pull", $remotePath, $destination)
    }

    $hashLines = @(Get-ChildItem -LiteralPath $backupDir -File |
        Where-Object { $_.Name -ne "sha256.txt" } |
        ForEach-Object {
            $hash = Get-FileHash -Algorithm SHA256 -LiteralPath $_.FullName
            "$($hash.Hash.ToLowerInvariant())  $($_.Name)"
        })
    Set-Content -LiteralPath (Join-Path $backupDir "sha256.txt") -Value $hashLines -Encoding ASCII
    Write-Host "Rollback snapshot: $backupDir"
}

if ($SkipBuild -and $BuildOnly) {
    throw "-SkipBuild and -BuildOnly cannot be used together."
}

Push-Location $projectDir
try {
    if (-not $SkipBuild) {
        Build-Installer
    } elseif (-not (Test-Path -LiteralPath $echoctlPath -PathType Leaf)) {
        throw "-SkipBuild was requested but $echoctlPath does not exist."
    }

    if ($BuildOnly) {
        Write-Host ""
        Write-Host "Build complete: $echoctlPath" -ForegroundColor Green
        return
    }

    Write-Section "Selecting and validating the Echo Dot"
    $adb = Resolve-Adb
    $targetSerial = Select-Biscuit -Adb $adb
    Assert-CompatibleBiscuit -Adb $adb -DeviceSerial $targetSerial

    Write-Section "Saving a rollback snapshot"
    Save-RollbackSnapshot -Adb $adb -DeviceSerial $targetSerial

    # echoctl and its Android subprocesses locate adb by name.
    $adbDirectory = Split-Path -Parent $adb
    if (($env:Path -split ';') -notcontains $adbDirectory) {
        $env:Path = "$adbDirectory;$env:Path"
    }

    Write-Section "Provisioning EchoLocal, Alexa, and ESPHome"
    $installArgs = @("install", "--serial", $targetSerial, "--reboot", "--yes")
    if ($Name) {
        $installArgs += @("--name", $Name)
    }
    Invoke-Program -FilePath $echoctlPath -ArgumentList $installArgs -Description "EchoLocal installation"

    Write-Section "Final device status"
    Invoke-Program -FilePath $echoctlPath `
        -ArgumentList @("status", "--serial", $targetSerial) `
        -Description "EchoLocal status"

    $key = Get-ProgramOutput -FilePath $echoctlPath `
        -ArgumentList @("key", "--serial", $targetSerial, "show") `
        -Description "reading the ESPHome encryption key"
    try {
        $keyBytes = [Convert]::FromBase64String($key)
    } catch {
        throw "echoctl returned an invalid ESPHome encryption key."
    }
    if ($keyBytes.Length -ne 32) {
        throw "echoctl returned a $($keyBytes.Length)-byte key; ESPHome requires 32 bytes."
    }

    Write-Host ""
    Write-Host "Provisioning complete." -ForegroundColor Green
    Write-Host "Home Assistant: Settings -> Devices & services -> ESPHome, select the discovered EchoLocal device."
    Write-Host "ESPHome encryption key (paste when prompted):" -ForegroundColor Green
    Write-Output $key
} finally {
    Pop-Location
}
