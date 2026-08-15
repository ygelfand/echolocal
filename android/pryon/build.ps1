[CmdletBinding()]
param(
    [string]$SdkRoot = "$env:LOCALAPPDATA\Android\Sdk",
    [string]$BuildToolsVersion = "36.0.0",
    [string]$PlatformVersion = "android-36",
    [string]$KeyStore = "$env:USERPROFILE\.android\debug.keystore"
)

$ErrorActionPreference = "Stop"
$projectDir = [IO.Path]::GetFullPath($PSScriptRoot)
$buildDir = [IO.Path]::GetFullPath((Join-Path $projectDir "build"))
$projectPrefix = $projectDir.TrimEnd([IO.Path]::DirectorySeparatorChar) `
        + [IO.Path]::DirectorySeparatorChar
if (-not $buildDir.StartsWith($projectPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to clean build directory outside project: $buildDir"
}

$toolsDir = Join-Path $SdkRoot "build-tools\$BuildToolsVersion"
$androidJar = Join-Path $SdkRoot "platforms\$PlatformVersion\android.jar"
$aapt = Join-Path $toolsDir "aapt.exe"
$d8 = Join-Path $toolsDir "d8.bat"
$zipalign = Join-Path $toolsDir "zipalign.exe"
$apksigner = Join-Path $toolsDir "apksigner.bat"
$required = @($androidJar, $aapt, $d8, $zipalign, $apksigner, $KeyStore)
foreach ($path in $required) {
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
        throw "Required build input is missing: $path"
    }
}

if (Test-Path -LiteralPath $buildDir) {
    Remove-Item -Recurse -Force -LiteralPath $buildDir
}
$classesDir = Join-Path $buildDir "classes"
$dexDir = Join-Path $buildDir "dex"
New-Item -ItemType Directory -Force -Path $classesDir, $dexDir | Out-Null

$sources = Get-ChildItem -Recurse -File -Filter "*.java" -LiteralPath (Join-Path $projectDir "src")
if ($sources.Count -eq 0) { throw "No Java sources found" }

& javac -source 8 -target 8 -Xlint:all -d $classesDir -cp $androidJar $sources.FullName
if ($LASTEXITCODE -ne 0) { throw "javac failed with exit code $LASTEXITCODE" }

$classFiles = Get-ChildItem -Recurse -File -Filter "*.class" -LiteralPath $classesDir
& $d8 --min-api 22 --lib $androidJar --output $dexDir $classFiles.FullName
if ($LASTEXITCODE -ne 0) { throw "d8 failed with exit code $LASTEXITCODE" }

$unsignedApk = Join-Path $buildDir "EchoLocalPryon.unsigned.apk"
$alignedApk = Join-Path $buildDir "EchoLocalPryon.aligned.apk"
$signedApk = Join-Path $buildDir "EchoLocalPryon.apk"
& $aapt package -f -M (Join-Path $projectDir "AndroidManifest.xml") `
        -F $unsignedApk -I $androidJar
if ($LASTEXITCODE -ne 0) { throw "aapt failed with exit code $LASTEXITCODE" }

Push-Location $dexDir
try {
    & $aapt add -f $unsignedApk "classes.dex"
    if ($LASTEXITCODE -ne 0) { throw "aapt add failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}

& $zipalign -f -p 4 $unsignedApk $alignedApk
if ($LASTEXITCODE -ne 0) { throw "zipalign failed with exit code $LASTEXITCODE" }

& $apksigner sign --ks $KeyStore --ks-key-alias androiddebugkey `
        --ks-pass pass:android --key-pass pass:android --out $signedApk $alignedApk
if ($LASTEXITCODE -ne 0) { throw "apksigner failed with exit code $LASTEXITCODE" }
& $apksigner verify --verbose --print-certs $signedApk
if ($LASTEXITCODE -ne 0) { throw "APK signature verification failed" }

$hash = Get-FileHash -Algorithm SHA256 -LiteralPath $signedApk
Write-Output "Built: $signedApk"
Write-Output "SHA256: $($hash.Hash.ToLowerInvariant())"
