[CmdletBinding()]
param(
    [string]$Ndk = $env:ANDROID_NDK_HOME
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$PSNativeCommandUseErrorActionPreference = $true

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

if (-not $Ndk) { throw 'no NDK: pass -Ndk or set ANDROID_NDK_HOME' }
$clang = Join-Path $Ndk 'toolchains\llvm\prebuilt\windows-x86_64\bin\clang.exe'
if (-not (Test-Path $clang)) { throw "no NDK clang at $clang" }
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { throw 'go is not on PATH' }

$bin = Join-Path $root 'bin'
$parts = Join-Path $root 'internal\parts\payload'
New-Item -ItemType Directory -Force $bin, $parts | Out-Null

$api = (go run ./cmd/native-target biscuit).Trim()
$native = Join-Path (go list -m -f '{{.Dir}}' github.com/ygelfand/libcountertop).Trim() 'native'

$target = @("--target=armv7a-linux-androideabi$api", '-fno-addrsig')
$flags = @('-O2', '-Wall', '-Werror', "-I$native", '-DCOUNTERTOP_PREFIX="echolocal"')

function Invoke-Clang([string[]]$arguments) { & $clang @target @arguments }

Write-Host "pryon (API $api)"
Invoke-Clang (@($flags) + @('-o', "$bin\echolocal-pryon", "$native\pryon\pryon.c", '-s', '-ldl'))
Copy-Item "$bin\echolocal-pryon" "$parts\echolocal-pryon"

Write-Host 'airplay'
$airplay = "$native\airplay"
$uxplay = "$airplay\uxplay"
$openssl = "$airplay\third_party\openssl"
$plist = "$airplay\third_party\libplist"

foreach ($dir in $openssl, $plist) {
    foreach ($line in Get-Content "$dir\SHA256SUMS") {
        if (-not $line.Trim()) { continue }
        $want, $file = $line -split '\s+\*?', 2
        $got = (Get-FileHash (Join-Path $dir $file) -Algorithm SHA256).Hash
        if ($got -ne $want) { throw "$dir\$file hashes to $got, want $want" }
    }
}

$stubs = "$bin\countertop-stubs\echolocal-api$api\airplay"
New-Item -ItemType Directory -Force $stubs | Out-Null
$includes = @("-I$uxplay", "-I$uxplay\playfair", "-I$uxplay\llhttp", "-I$uxplay\mdnsd",
    "-I$openssl\include", "-I$plist\include")
$uxflags = @('-O2', '-fPIC', '-w', '-D_GNU_SOURCE', '-DPLIST_210', '-DPLIST_230') + $includes

Invoke-Clang @('-shared', '-DLIBBINDER', '-Wl,-soname,libbinder.so', '-o', "$stubs\libbinder.so",
    "$native\camera\stubs.c")

$sources = foreach ($dir in '', 'playfair', 'llhttp', 'mdnsd') {
    Get-ChildItem (Join-Path $uxplay $dir) -Filter *.c -File
}
foreach ($src in $sources) {
    $relative = $src.FullName.Substring($uxplay.Length + 1) -replace '[\\/]', '_'
    Invoke-Clang (@($uxflags) + @('-c', $src.FullName, '-o', "$stubs\$($relative -replace '\.c$', '.o')"))
}
Invoke-Clang (@($uxflags) + @('-c', "$airplay\third_party\alac\alac.c", '-o', "$stubs\alac.o"))

$objects = (Get-ChildItem $stubs -Filter *.o).FullName
Invoke-Clang (@($flags) + $includes + @('-o', "$bin\echolocal-airplay", "$airplay\airplay.c",
    "$airplay\aac.c") + $objects + @("$plist\android-armv7\libplist-2.0.a",
    "$openssl\android-armv7\libcrypto.a", '-s', "-L$stubs", '-lbinder', '-ldl', '-llog', '-lm',
    '-Wl,--allow-shlib-undefined'))
Copy-Item "$bin\echolocal-airplay" "$parts\echolocal-airplay"

$commit = (git rev-parse --short HEAD).Trim()
$dirty = if (git status --porcelain) { 'dirty' } else { $commit }
$epoch = [DateTimeOffset]::UtcNow.ToUnixTimeSeconds()
$version = "$((Get-Content VERSION).Trim())-dev.${epoch}_$dirty"
$layout = 'github.com/ygelfand/echolocal/internal/layout'
$ldflags = "-X '$layout.Version=$version' -X '$layout.GitCommit=$commit' " +
    "-X '$layout.BuildDate=$([DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ'))'"

$saved = $env:GOOS, $env:GOARCH, $env:CGO_ENABLED
try {
    $env:CGO_ENABLED = '0'

    Write-Host "echolocal.exe $version"
    $env:GOOS, $env:GOARCH = 'windows', 'amd64'
    go build -ldflags $ldflags -o "$bin\echolocal.exe" ./cmd/echolocal

    Write-Host "echod-arm $version"
    $env:GOOS, $env:GOARCH = 'linux', 'arm'
    go build -tags 'board_biscuit,airplay,pryon' -ldflags "-s -w $ldflags" -o "$bin\echod-arm" ./cmd/echod
}
finally {
    $env:GOOS, $env:GOARCH, $env:CGO_ENABLED = $saved
}

Write-Host "built $bin\echolocal.exe and $bin\echod-arm"
