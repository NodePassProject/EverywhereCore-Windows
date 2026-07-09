package main

import (
	"github.com/xtls/xray-core/core"

	// Register inbound, outbound, transport handlers via init().
	_ "github.com/xtls/xray-core/main/distro/all"
	// Register the JSON config loader. Importing main/json transitively
	// pulls in infra/conf, which registers `tun` as an inbound type and
	// brings in proxy/tun whose init() registers the handler.
	_ "github.com/xtls/xray-core/main/json"
)

type xrayRunner struct {
	instance *core.Instance
}

func (x *xrayRunner) stop() error {
	if x.instance != nil {
		return x.instance.Close()
	}
	return nil
}

// suspend / resume are no-ops: xray-core has no public pause hook.
// The C# side still calls them on every core to keep the bridge
// surface uniform.
func (x *xrayRunner) suspend() {}
func (x *xrayRunner) resume()  {}

// startXray boots Xray with the TUN inbound declared in the JSON.
// Xray's Windows TUN path (proxy/tun/tun_windows.go) creates a WinTUN
// adapter named by the inbound's `settings.name` (default "xray0") —
// no FD env var, no injection from out here. WinTUN loads from
// wintun.dll, which for the zx2c4 loader Xray uses must sit next to
// the host executable; see PATCHES.md.
func startXray(configContent string) (coreRunner, error) {
	inst, err := core.StartInstance("json", []byte(configContent))
	if err != nil {
		return nil, err
	}
	return &xrayRunner{instance: inst}, nil
}
