// Package main builds as a c-shared DLL (EverywhereCore.dll) — the
// Windows entry point for Everywhere's networking stack. It boots one
// of three upstream proxy cores — Xray, sing-box, or mihomo — that
// all share a single Go runtime inside the host process.
//
// Each core owns its own TUN inbound. Unlike the iOS and Android
// siblings there is no file descriptor to inject: on Windows the
// cores create their WinTUN adapter themselves from the name declared
// in the configuration, so StartCore takes only the core type and the
// config text. The host process needs Administrator rights (or the
// SeLoadDriverPrivilege arrangement a service gets as LocalSystem)
// for WinTUN adapter creation.
//
// The C# side calls (through the flat C API in exports.go, in order):
//
//	EvcoreSetResourcesPath(path)             // optional, asset dir
//	EvcoreStartCore(coreType, configContent) // boots the proxy core
//
// While running:
//
//	EvcoreSuspend()                          // on system suspend
//	EvcoreResume()                           // on resume
//
// On teardown:
//
//	EvcoreStopAll()
//
// There is no UpdateDefaultInterface on Windows: all three cores run
// their own default-route monitoring here (sing-box and mihomo via
// sing-tun's winipcfg watcher, plus sing-box's winpowrprof power
// listener), so no external feed is needed.
//
// The provided configuration must declare a TUN inbound for the
// active core; ConfigNormalizer on the C# side handles that.
package main

import (
	"errors"
	"fmt"
	"sync"
)

const (
	CoreTypeXray    = "xray"
	CoreTypeSingBox = "singbox"
	CoreTypeMihomo  = "mihomo"
)

var (
	mu           sync.Mutex
	coreInstance coreRunner
)

type coreRunner interface {
	stop() error
	// suspend pauses non-essential activity in the running core
	// (URL-test probes, keepalives, new-connection handling) until
	// resume is called. Cores with no native pause hook (xray) leave
	// this as a no-op.
	suspend()
	resume()
}

func Version() string { return "Everywhere Core Windows v0.1" }

// StartCore boots the chosen proxy core. The configuration's TUN
// inbound names the WinTUN adapter the core creates and owns.
func StartCore(coreType, configContent string) error {
	mu.Lock()
	defer mu.Unlock()
	if coreInstance != nil {
		return errors.New("a core is already running")
	}
	var (
		r   coreRunner
		err error
	)
	switch coreType {
	case CoreTypeXray:
		r, err = startXray(configContent)
	case CoreTypeSingBox:
		r, err = startSingBox(configContent)
	case CoreTypeMihomo:
		r, err = startMihomo(configContent)
	default:
		return fmt.Errorf("unknown core type: %s", coreType)
	}
	if err != nil {
		// Flatten to a plain *errorString so the C layer only ever
		// formats a message — mirrors the iOS/Android bridges, where
		// boxing non-comparable error values crashes the bridge.
		return errors.New(err.Error())
	}
	coreInstance = r
	return nil
}

// Suspend pauses non-essential activity of the running core. Drive it
// from the service's power-event handling (SERVICE_CONTROL_POWEREVENT
// / PBT_APMSUSPEND) — without that hop, mihomo keeps handling new
// connections through a suspend transition and wireguard keepalives
// fire the moment the machine wakes into a dead network. sing-box
// additionally registers its own winpowrprof listener, for which this
// is a harmless double-tap. Returns nil when no core is running.
func Suspend() error {
	mu.Lock()
	defer mu.Unlock()
	if coreInstance == nil {
		return nil
	}
	coreInstance.suspend()
	return nil
}

// Resume reverses Suspend (PBT_APMRESUMEAUTOMATIC).
func Resume() error {
	mu.Lock()
	defer mu.Unlock()
	if coreInstance == nil {
		return nil
	}
	coreInstance.resume()
	return nil
}

// StopAll halts the running core and returns once teardown finishes.
//
// Deliberately synchronous, unlike the iOS/Android siblings: there
// the OS reclaims the whole extension/service process moments after
// stop returns, so those builds detach teardown and let the process
// die. A Windows service hosts many start/stop cycles in one long-
// lived process — returning before the core has released its WinTUN
// adapter, routes, and listen ports would make an immediate restart
// fail on "adapter already exists" / "address already in use".
// Worst-case latency is bounded by the cores' own close paths (Xray
// drains outbounds, sing-box caps services at 10s, mihomo cleans up
// DNS/listeners); call it off the request thread.
func StopAll() error {
	mu.Lock()
	prev := coreInstance
	coreInstance = nil
	mu.Unlock()

	if prev == nil {
		return nil
	}
	defer func() { _ = recover() }()
	if err := prev.stop(); err != nil {
		return errors.New(err.Error())
	}
	return nil
}
