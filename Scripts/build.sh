#!/usr/bin/env bash
# Cross-compiles EverywhereCore.dll (+ generated EverywhereCore.h)
# from macOS/Linux. buildmode=c-shared needs cgo, so a Windows C
# toolchain must be present per target arch:
#
#   amd64:  x86_64-w64-mingw32-gcc     (brew install mingw-w64 /
#                                       apt install gcc-mingw-w64-x86-64)
#   arm64:  aarch64-w64-mingw32-clang  (llvm-mingw release tarball)
#
# Arches whose compiler is missing are skipped with a warning, so a
# stock `brew install mingw-w64` machine still produces the amd64 DLL
# everyone actually ships. On a Windows machine use Scripts/build.ps1
# instead.
#
# The three core dependencies (mihomo, sing-box, xray-core) are
# resolved from the Go module proxy via go.mod, not vendored. Upstream
# version bumps land here through .github/workflows/upstream-watch.yml.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CORE_DIR="$ROOT/go"
DIST="$ROOT/dist"

cd "$CORE_DIR"

# --- Symmetric varz patch -------------------------------------------------
# Two tailscale forks live in the dep graph: github.com/sagernet/tailscale
# (sing-box, gated on with_tailscale) and github.com/metacubex/tailscale
# (mihomo, unconditional). Each fork's tsweb/varz/init() registers five
# expvars with hardcoded names ("process_start_unix_time", "version",
# "go_version", "counter_uptime_sec", "gauge_goroutines"). expvar.Publish
# panics on duplicate names — two distinct module paths run two distinct
# init()s against the same process-global registry → boom on the second.
#
# We patch each fork in a writable copy of its module-cache source,
# prefix each published name with the fork's vendor so they coexist,
# and add `replace` directives so the build links the patched copies.
# Replaces are dropped on EXIT so go.mod stays clean in version control.
PATCHED_ROOT="$CORE_DIR/.patched"

cleanup_replaces() {
    go mod edit -dropreplace=github.com/sagernet/tailscale  2>/dev/null || true
    go mod edit -dropreplace=github.com/metacubex/tailscale 2>/dev/null || true
}
trap cleanup_replaces EXIT
# Start clean in case a prior interrupted run left a replace behind.
cleanup_replaces

echo "→ go mod tidy"
go mod tidy

apply_varz_patch() {
    local module="$1" prefix="$2" dirname="$3"
    local version
    version="$(go list -m -f '{{.Version}}' "$module" 2>/dev/null || true)"
    if [[ -z "$version" ]]; then
        echo "→ $module not in deps; skipping varz patch"
        return
    fi
    local gomodcache src dest
    gomodcache="$(go env GOMODCACHE)"
    src="$gomodcache/$module@$version"
    dest="$PATCHED_ROOT/$dirname@$version"
    if [[ ! -d "$src" ]]; then
        go mod download "$module@$version"
    fi
    # Cache by version: if already patched at this version, reuse.
    if [[ ! -f "$dest/tsweb/varz/varz.go" ]]; then
        rm -rf "$dest"
        mkdir -p "$dest"
        cp -R "$src/" "$dest/"
        chmod -R u+w "$dest"
        sed -i.bak \
            -e "s/expvar\.Publish(\"process_start_unix_time\"/expvar.Publish(\"${prefix}_process_start_unix_time\"/" \
            -e "s/expvar\.Publish(\"version\"/expvar.Publish(\"${prefix}_version\"/" \
            -e "s/expvar\.Publish(\"go_version\"/expvar.Publish(\"${prefix}_go_version\"/" \
            -e "s/expvar\.Publish(\"counter_uptime_sec\"/expvar.Publish(\"${prefix}_counter_uptime_sec\"/" \
            -e "s/expvar\.Publish(\"gauge_goroutines\"/expvar.Publish(\"${prefix}_gauge_goroutines\"/" \
            "$dest/tsweb/varz/varz.go"
        rm -f "$dest/tsweb/varz/varz.go.bak"
    fi
    go mod edit -replace "$module=$dest"
    echo "→ patched $module@$version (prefix=${prefix}_)"
}

apply_varz_patch github.com/sagernet/tailscale  sagernet  sagernet-tailscale
apply_varz_patch github.com/metacubex/tailscale metacubex metacubex-tailscale

# Build tags enable optional features in upstream cores. We ship the
# same subset as the iOS/Android frameworks — the Windows service is
# just as client-only. Inbound/server-only tags and big-tree extras
# (anthropic/openai SDK service registries, ACME issuance, v2ray
# stats gRPC, DHCP DNS probing) are dropped. See PATCHES.md.
BUILD_TAGS="\
with_clash_api \
with_grpc \
with_gvisor \
with_quic \
with_tailscale \
with_utls \
with_wireguard"

# -s: strip Go symbol table.  -w: strip DWARF. Panics still print Go
# stacks through the runtime's own handler.
LDFLAGS="-s -w"

built_any=false

build_arch() {
    local goarch="$1" cc="$2"
    if ! command -v "$cc" >/dev/null 2>&1; then
        echo "→ skipping windows/$goarch ($cc not found)"
        return
    fi
    local out="$DIST/windows-$goarch"
    echo "→ building windows/$goarch"
    mkdir -p "$out"
    CGO_ENABLED=1 GOOS=windows GOARCH="$goarch" CC="$cc" \
        go build -trimpath -buildmode=c-shared \
        -tags="$(echo $BUILD_TAGS)" \
        -ldflags="$LDFLAGS" \
        -o "$out/EverywhereCore.dll" .
    built_any=true
    du -sh "$out/EverywhereCore.dll"
}

build_arch amd64 x86_64-w64-mingw32-gcc
build_arch arm64 aarch64-w64-mingw32-clang

if [[ "$built_any" != true ]]; then
    echo "error: no Windows C toolchain found; install mingw-w64 (amd64) and/or llvm-mingw (arm64)" >&2
    exit 1
fi

echo "✓ built under $DIST"
