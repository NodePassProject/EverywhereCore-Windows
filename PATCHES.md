# Patches

Ledger of wiring quirks and c-shared mechanics that make the three
upstreams co-exist in one DLL on Windows. The cores themselves are
unpatched — if that ever changes, see "Future source patches" at the
bottom.

Pinned versions live in `go/go.mod`. The daily upstream-watch workflow
keeps them current; see `README.md` for the release flow.

## Build mechanics

### `-buildmode=c-shared` needs a GNU-flavoured toolchain

cgo cannot drive MSVC. Native builds need MSYS2's mingw-w64 or
llvm-mingw on PATH; cross builds from macOS/Linux use
`x86_64-w64-mingw32-gcc` (amd64) / `aarch64-w64-mingw32-clang`
(arm64). The DLL exports use the default C calling convention for the
target (effectively cdecl); C# must declare
`CallingConvention.Cdecl`.

### Returned strings cross the heap boundary

Every `char*` the DLL returns is `C.CString` — C-heap memory the
caller owns. `EvcoreFreeString` is exported so the host frees with
the same allocator that malloc'd. Never `Marshal.FreeHGlobal` these.

**On upstream bump.** No action — these are cgo mechanics.

## Source patches

Xray-core, sing-box, and mihomo all build unmodified from their
published Go-module tags. The only sources we modify are two
transitive deps that fight over `expvar.Publish`.

### tailscale forks: rename five expvar names to coexist

Identical to the iOS/Android frameworks' patch — the collision is
process-global and platform-independent. Two tailscale forks land in
the dep graph and each runs its own init():

- `github.com/sagernet/tailscale` — pulled in by sing-box's DERP
  service when `with_tailscale` is enabled.
- `github.com/metacubex/tailscale` — pulled in by mihomo
  unconditionally.

Both forks ship a copy of `tsweb/varz/varz.go` whose init() calls
`expvar.Publish` five times with hardcoded names
(`process_start_unix_time`, `version`, `go_version`,
`counter_uptime_sec`, `gauge_goroutines`). `expvar.Publish` panics on
the duplicate registration from whichever init() runs second — for a
c-shared build that means the panic fires while the Go runtime boots
on the host's *first* P/Invoke call.

Both build scripts rewrite both copies after `go mod tidy`:

1. Resolve each fork's version via `go list -m`.
2. Copy the module-cache source into `go/.patched/<vendor>-tailscale@<version>/`.
3. Rewrite the five `expvar.Publish(...)` calls in
   `tsweb/varz/varz.go` to prefix the published name with the vendor.
4. `go mod edit -replace` so the build links the patched copies.

Patched directories are version-suffixed and cached across builds.
The replace directives are dropped afterwards (EXIT trap in build.sh,
`finally` in build.ps1) so go.mod stays clean in version control.
`.patched/` is gitignored. **Keep build.sh and build.ps1 in sync** —
build.sh is the authoritative copy the release workflow runs.

**On upstream bump.** The patch is keyed on the resolved module
versions, so a new sing-box or mihomo tag that bumps either fork just
regenerates `.patched/<vendor>-tailscale@<new-version>/`.

## Wiring quirks per core

These are not patches but call-site requirements that the wrappers in
`go/` already encode. Listed here so they survive a future rewrite.

### TUN inbound: each core opens its own WinTUN adapter

The iOS/Android editions inject an OS-created tun fd into each core.
Windows has no such handoff — each core creates the adapter itself
from its config, which is why `StartCore` here takes no fd/mtu and
why the host must run elevated:

- **Xray-core**: `proxy/tun/tun_windows.go` opens/creates a WinTUN
  adapter named by the tun inbound's `settings.name` (default
  `xray0`) and drives it through gVisor. It uses
  `golang.zx2c4.com/wintun`, which loads an **external
  `wintun.dll`** — ship the signed DLL from wintun.net next to the
  host exe. Note Xray does not configure addresses/routes on the
  adapter and takes WinTUN's default MTU (there's no iphlpapi
  plumbing in its Windows path); the host service is responsible for
  addressing/routing the adapter, e.g. via `netsh`/iphlpapi after
  start.
- **sing-box**: the tun inbound via sagernet/sing-tun, which
  **embeds** wintun.dll (memmod loader — nothing on disk), assigns
  the configured `address`, applies `auto_route`, and watches the
  default route through winipcfg. Runs exactly like the standalone
  sing-box.exe — no `adapter.PlatformInterface` is installed, unlike
  the iOS/Android editions.
- **mihomo**: same story through metacubex/sing-tun (also embedded
  wintun.dll): `tun.device` names the adapter, `auto-route` /
  `auto-detect-interface` work natively. `listener/sing_tun/
  server_windows.go` retries adapter creation for the
  "file already exists" race and forces bind-to-interface on
  pre-Win10.

### No UpdateDefaultInterface / no socket protection

The iOS edition feeds NWPathMonitor changes in, and the Android
edition additionally protects every socket via VpnService.protect.
Windows needs neither: there is no protect() concept (auto_route
excludes the core's own traffic via routing rules and
bind-to-interface), and sing-box/mihomo watch route changes natively
(sing-box also registers a winpowrprof power listener). The exported
Suspend/Resume remain useful for deterministic service-driven
suspend handling; for sing-box they double-tap the same pause
manager its power listener uses, which is harmless.

### sing-box: gomobile-era `with_*` tags still apply

Same tag set as the iOS/Android frameworks — the Windows service is
just as client-only:

`with_clash_api with_grpc with_gvisor with_quic with_tailscale
with_utls with_wireguard`

Excluded (same reasoning as iOS, see that repo's PATCHES.md for the
full table): `with_acme`, `with_ccm`, `with_dhcp`, `with_ech`
(deprecated), `with_naive_outbound`, `with_ocm`,
`with_reality_server` (deprecated), `with_v2ray_api`. cronet ships
windows_amd64/arm64 libs, so `with_naive_outbound` *could* be enabled
here; it stays off for parity until an Everywhere feature needs it.

### sing-box: must pass `include.Context(ctx)` into `box.New`

sing-box 1.10+ requires the inbound/outbound/endpoint/DNS-transport/
service registries to be attached to the context that `box.New` is
called with; `include.Context(ctx)` bundles them in one call. Without
it box.New parses the JSON but cannot instantiate `socks`, `direct`,
`vmess`, …, and start fails immediately.

**On upstream bump.** Verify `include.Context` is still the canonical
entry point — the registry surface has been refactored a couple of
times in 1.x.

### mihomo: must call `hub.ApplyConfig`, not `executor.ApplyConfig`

`executor.ApplyConfig` alone does not start the external-controller
HTTP/WS API server; `hub.ApplyConfig` wraps `applyRoute` (which boots
it) plus `executor.ApplyConfig`. Without the hub call, yacd shows
"cannot connect to 127.0.0.1:9090". `hub.ApplyConfig` returns no
error — failures inside it are logged via mihomo's own logger.

### StopAll is synchronous here

iOS kills the NE process right after stopTunnel and Android tears the
fd out from under the cores, so those editions detach teardown. A
Windows service process hosts many start/stop cycles; StopAll only
returns once the core has released its WinTUN adapter, routes, and
ports, so an immediate StartCore cannot collide with the previous
instance. See `go/core.go` for the latency bounds.

## Future source patches

The Go module cache is read-only by design, so patching upstream
sources in place is not an option. Two paths exist depending on the
size of the change:

**Build-time rewrite (preferred for tiny, line-level fixes).** Copy
the module-cache source into `go/.patched/<vendor>-<repo>@<version>/`,
apply the rewrite in both build scripts, and add a transient
`replace` directive dropped after the build. See the tailscale-forks
entry for the template.

**GitHub fork (for anything bigger).** Fork to
`github.com/NodePassProject/<repo>`, add a `replace` directive to
`go/go.mod`, document **why**, **what file**, and **what the
upstream-correct fix would be** here, and update
`.github/workflows/upstream-watch.yml` to watch the fork.
