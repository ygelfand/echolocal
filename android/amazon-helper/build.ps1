[CmdletBinding()]
param(
    [string]$SdkRoot = "$env:LOCALAPPDATA\Android\Sdk",
    [string]$BuildToolsVersion = "36.0.0",
    [string]$PlatformVersion = "android-36"
)

$ErrorActionPreference = "Stop"
$projectDir = [IO.Path]::GetFullPath($PSScriptRoot)
$buildDir = [IO.Path]::GetFullPath((Join-Path $projectDir "build"))
$projectPrefix = $projectDir.TrimEnd([IO.Path]::DirectorySeparatorChar) `
        + [IO.Path]::DirectorySeparatorChar
if (-not $buildDir.StartsWith($projectPrefix, [StringComparison]::OrdinalIgnoreCase)) {
    throw "Refusing to clean build directory outside project: $buildDir"
}

$androidJar = Join-Path $SdkRoot "platforms\$PlatformVersion\android.jar"
$d8 = Join-Path $SdkRoot "build-tools\$BuildToolsVersion\d8.bat"
foreach ($path in @($androidJar, $d8)) {
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
& javac -source 8 -target 8 -Xlint:all -d $classesDir -cp $androidJar $sources.FullName
if ($LASTEXITCODE -ne 0) { throw "javac failed with exit code $LASTEXITCODE" }

$classFiles = Get-ChildItem -Recurse -File -Filter "*.class" -LiteralPath $classesDir
& $d8 --min-api 22 --lib $androidJar --output $dexDir $classFiles.FullName
if ($LASTEXITCODE -ne 0) { throw "d8 failed with exit code $LASTEXITCODE" }

$jarPath = Join-Path $buildDir "amazon-helper.jar"
Push-Location $dexDir
try {
    & jar cf $jarPath "classes.dex"
    if ($LASTEXITCODE -ne 0) { throw "jar failed with exit code $LASTEXITCODE" }
} finally {
    Pop-Location
}

$hash = Get-FileHash -Algorithm SHA256 -LiteralPath $jarPath
Write-Output "Built: $jarPath"
Write-Output "SHA256: $($hash.Hash.ToLowerInvariant())"
