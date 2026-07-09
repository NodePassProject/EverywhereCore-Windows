# EverywhereCore-Windows

Go core (mihomo + sing-box + xray-core) packaged as a c-shared
`EverywhereCore.dll` with a flat C API. Consumed by the Everywhere
Windows service via P/Invoke. Sibling of
[EverywhereCore](https://github.com/NodePassProject/EverywhereCore)
(iOS/macOS xcframework) and EverywhereCore-Android (gomobile aar) —
same wrapper design, same upstream pins.

Each core runs its own TUN inbound on a WinTUN adapter it creates
itself — there is no userland tun→socks shim, and (unlike iOS and
Android) no file descriptor to inject: on Windows the adapter is
opened by name from the config. The host process therefore needs
Administrator rights; a service running as LocalSystem is the
intended host.

The three core dependencies are not vendored. `go/go.mod` pins them
by Go-module-compatible semver; the GitHub Actions workflow at
`.github/workflows/upstream-watch.yml` polls the Go proxy daily and
auto-cuts a new release whenever any upstream moves.

## Layout

```
go/                        c-shared entry package; *.go + go.mod
Scripts/build.sh           cross-compile from macOS/Linux (mingw)
Scripts/build.ps1          native build on Windows
.github/workflows/
  upstream-watch.yml       daily upstream poll + auto-release
```

## Building

On macOS/Linux (cross-compile; `brew install mingw-w64` or
`apt install gcc-mingw-w64-x86-64` first, llvm-mingw for arm64):

```sh
Scripts/build.sh           # → dist/windows-amd64/EverywhereCore.dll (+.h)
```

On Windows (MSYS2 mingw-w64 or llvm-mingw on PATH — cgo cannot use
MSVC):

```powershell
powershell -ExecutionPolicy Bypass -File Scripts\build.ps1            # amd64
powershell -ExecutionPolicy Bypass -File Scripts\build.ps1 -Arch arm64
```

Output is `dist/windows-<arch>/EverywhereCore.dll` plus the
cgo-generated `EverywhereCore.h`.

## C API

Declared in the generated `EverywhereCore.h`. All fallible functions
return `char*`: `NULL` on success, a UTF-8 error message on failure.
Every non-NULL string the DLL returns must be released with
`EvcoreFreeString`.

```c
char* EvcoreVersion(void);
char* EvcoreSetResourcesPath(char* path);
char* EvcoreStartCore(char* coreType, char* configContent);
char* EvcoreSuspend(void);
char* EvcoreResume(void);
char* EvcoreStopAll(void);
void  EvcoreFreeString(char* s);
```

`coreType` is `"xray"`, `"singbox"`, or `"mihomo"`. The config must
declare a TUN inbound for the active core (the service-side
ConfigNormalizer takes care of that): a `tun` inbound for Xray
(`settings.name` names the WinTUN adapter, default `xray0`), a
`type: tun` inbound for sing-box, an enabled `tun:` block for mihomo.

`EvcoreStopAll` is synchronous — it returns once the core has
released its WinTUN adapter, routes, and ports, so an immediate
restart is safe. Call it off the request thread (worst case is
bounded at roughly 10 s by sing-box's service-close timeout).

There is no `UpdateDefaultInterface` in this edition: all three cores
monitor the default route natively on Windows, so the iOS/Android
network-change feed has no equivalent here.

## Calling from C#

```csharp
internal static class Evcore
{
    private const string Dll = "EverywhereCore.dll";

    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern IntPtr EvcoreVersion();
    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern IntPtr EvcoreSetResourcesPath([MarshalAs(UnmanagedType.LPUTF8Str)] string path);
    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern IntPtr EvcoreStartCore([MarshalAs(UnmanagedType.LPUTF8Str)] string coreType, [MarshalAs(UnmanagedType.LPUTF8Str)] string config);
    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern IntPtr EvcoreSuspend();
    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern IntPtr EvcoreResume();
    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern IntPtr EvcoreStopAll();
    [DllImport(Dll, CallingConvention = CallingConvention.Cdecl)] private static extern void EvcoreFreeString(IntPtr s);

    private static void ThrowOnError(IntPtr err)
    {
        if (err == IntPtr.Zero) return;
        var message = Marshal.PtrToStringUTF8(err)!;
        EvcoreFreeString(err);
        throw new InvalidOperationException(message);
    }

    public static void StartCore(string coreType, string config) => ThrowOnError(EvcoreStartCore(coreType, config));
    public static void StopAll() => ThrowOnError(EvcoreStopAll());
    public static void SetResourcesPath(string path) => ThrowOnError(EvcoreSetResourcesPath(path));
    public static void Suspend() => ThrowOnError(EvcoreSuspend());
    public static void Resume() => ThrowOnError(EvcoreResume());
}
```

Wire `Suspend`/`Resume` to the service's
`SERVICE_CONTROL_POWEREVENT` handling (`PBT_APMSUSPEND` /
`PBT_APMRESUMEAUTOMATIC`).

### wintun.dll

sing-box and mihomo embed WinTUN and need nothing on disk. **Xray
does not** — it loads `wintun.dll` at runtime, so place the signed
DLL from [wintun.net](https://www.wintun.net) next to the host
executable if the Xray core is offered. Details in PATCHES.md.

## How releases happen

`.github/workflows/upstream-watch.yml` runs daily at 08:00 UTC and on
manual `workflow_dispatch`. Each run:

1. Queries `proxy.golang.org/<module>/@latest` for mihomo, sing-box,
   and xray-core (stable tags only — no pre-releases).
2. Compares each against the version currently pinned in `go/go.mod`.
3. If at least one is newer (or if dispatched with `force_release: true`):
   - `go get` each to its latest, `go mod tidy`
   - `Scripts/build.sh` to build amd64 + arm64 DLLs
   - zip each `dist/windows-<arch>/` pair, compute SHA256s,
     commit + tag `vYYYY.MM.DD`
   - Append `.1`, `.2`, … to the tag if multiple runs land same day
   - Push tag + main; `gh release create` with the zips attached
4. Otherwise: no-op (logged as a notice).

To bootstrap the first release after pushing this repo, run the
workflow from the Actions tab with `force_release: true`.

## Pinning a specific upstream version manually

Edit `go/go.mod`, push to main. The next cron run will detect the
manual pin is current (or stale) and act accordingly. If you want a
release for a manual bump immediately, dispatch the workflow with
`force_release: true`.
