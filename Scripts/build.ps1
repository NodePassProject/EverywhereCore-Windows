# Builds EverywhereCore.dll (+ generated EverywhereCore.h) natively on
# Windows. buildmode=c-shared needs cgo, so a GCC/Clang-flavoured
# Windows toolchain must be on PATH — MSVC cannot compile cgo:
#
#   winget install MSYS2.MSYS2      # then: pacman -S mingw-w64-x86_64-gcc
#   # or the llvm-mingw release zip (also covers arm64 cross builds)
#
# Usage:  powershell -ExecutionPolicy Bypass -File Scripts\build.ps1 [-Arch amd64|arm64]
#
# Mirrors Scripts/build.sh (the macOS/Linux cross-compile flavour);
# keep the two in sync — especially BUILD_TAGS and the varz patch.

param(
    [ValidateSet("amd64", "arm64")]
    [string]$Arch = "amd64"
)

$ErrorActionPreference = "Stop"

$Root = Split-Path -Parent $PSScriptRoot
$CoreDir = Join-Path $Root "go"
$Dist = Join-Path $Root "dist\windows-$Arch"

Set-Location $CoreDir

# --- Symmetric varz patch -------------------------------------------------
# Both tailscale forks (sagernet's via sing-box + with_tailscale,
# metacubex's via mihomo unconditionally) publish the same five expvar
# names from init(); expvar.Publish panics on the duplicate. Patch
# writable copies of both, prefixing the names with the vendor, and
# link them via transient `replace` directives. Full rationale in
# PATCHES.md; authoritative implementation in Scripts/build.sh.
$PatchedRoot = Join-Path $CoreDir ".patched"

function Remove-Replaces {
    go mod edit -dropreplace=github.com/sagernet/tailscale  2>$null
    go mod edit -dropreplace=github.com/metacubex/tailscale 2>$null
}

function Apply-VarzPatch([string]$Module, [string]$Prefix, [string]$DirName) {
    $version = (go list -m -f '{{.Version}}' $Module) 2>$null
    if (-not $version) {
        Write-Host "-> $Module not in deps; skipping varz patch"
        return
    }
    $gomodcache = (go env GOMODCACHE)
    $src = Join-Path $gomodcache "$Module@$version".Replace('/', [IO.Path]::DirectorySeparatorChar)
    $dest = Join-Path $PatchedRoot "$DirName@$version"
    if (-not (Test-Path $src)) {
        go mod download "$Module@$version"
    }
    $varz = Join-Path $dest "tsweb\varz\varz.go"
    if (-not (Test-Path $varz)) {
        if (Test-Path $dest) { Remove-Item -Recurse -Force $dest }
        Copy-Item -Recurse $src $dest
        # The module cache is read-only; the copy inherits that.
        Get-ChildItem -Recurse -File $dest | ForEach-Object { $_.IsReadOnly = $false }
        $content = Get-Content -Raw $varz
        foreach ($name in @("process_start_unix_time", "version", "go_version", "counter_uptime_sec", "gauge_goroutines")) {
            $content = $content.Replace("expvar.Publish(`"$name`"", "expvar.Publish(`"${Prefix}_$name`"")
        }
        Set-Content -NoNewline -Path $varz -Value $content
    }
    go mod edit -replace "$Module=$dest"
    Write-Host "-> patched $Module@$version (prefix=${Prefix}_)"
}

# Start clean in case a prior interrupted run left a replace behind.
Remove-Replaces

try {
    Write-Host "-> go mod tidy"
    go mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed" }

    Apply-VarzPatch "github.com/sagernet/tailscale"  "sagernet"  "sagernet-tailscale"
    Apply-VarzPatch "github.com/metacubex/tailscale" "metacubex" "metacubex-tailscale"

    # Same client-only tag subset as the iOS/Android frameworks; see
    # PATCHES.md for the include/exclude rationale.
    $BuildTags = "with_clash_api with_grpc with_gvisor with_quic with_tailscale with_utls with_wireguard"

    New-Item -ItemType Directory -Force -Path $Dist | Out-Null

    $env:CGO_ENABLED = "1"
    $env:GOOS = "windows"
    $env:GOARCH = $Arch
    Write-Host "-> go build c-shared windows/$Arch tags=$BuildTags"
    go build -trimpath -buildmode=c-shared `
        -tags="$BuildTags" `
        -ldflags="-s -w" `
        -o (Join-Path $Dist "EverywhereCore.dll") .
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }

    Write-Host "OK: built $(Join-Path $Dist 'EverywhereCore.dll')"
}
finally {
    # Drop the replaces so go.mod stays clean in version control.
    Remove-Replaces
}
