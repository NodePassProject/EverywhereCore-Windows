package main

import (
	"errors"

	"github.com/metacubex/mihomo/hub"
	"github.com/metacubex/mihomo/hub/executor"
	"github.com/metacubex/mihomo/tunnel"
)

type mihomoRunner struct{}

func (m *mihomoRunner) stop() error {
	executor.Shutdown()
	return nil
}

// suspend stops mihomo's tunnel from accepting new TCP/UDP from the
// TUN stack — handleTCPConn/handleUDPConn check tunnel.Status() and
// drop everything that isn't Running. Existing in-flight connections
// keep going; only newly arriving packets are short-circuited until
// resume.
func (m *mihomoRunner) suspend() {
	tunnel.OnSuspend()
}

func (m *mihomoRunner) resume() {
	tunnel.OnRunning()
}

// startMihomo boots mihomo with the TUN inbound declared in the YAML.
// On Windows mihomo opens its own WinTUN adapter (metacubex/sing-tun
// ships the dll embedded), applies auto-route, and monitors the
// default interface natively — so unlike the iOS/Android siblings
// nothing is injected into the parsed config beyond the checks below.
func startMihomo(configContent string) (coreRunner, error) {
	cfg, err := executor.ParseWithBytes([]byte(configContent))
	if err != nil {
		return nil, err
	}
	if cfg.General == nil {
		return nil, errors.New("mihomo: parsed config has no general block")
	}
	if !cfg.General.Tun.Enable {
		return nil, errors.New("mihomo: tun block is missing or disabled")
	}

	// mihomo's DefaultRawConfig sets DNSHijack to ["0.0.0.0:53"],
	// catching every DNS query at the TUN stack and routing it to
	// resolver.DefaultService. When the user's config doesn't
	// `dns.enable: true`, DefaultService is nil and every query
	// returns SERVFAIL. Drop the hijack list whenever DNS isn't
	// enabled so queries flow through as plain UDP traffic —
	// matching Xray and sing-box, which don't hijack DNS by default
	// in this app.
	if cfg.DNS == nil || !cfg.DNS.Enable {
		cfg.General.Tun.DNSHijack = nil
	}

	// hub.ApplyConfig does both applyRoute (which boots the
	// external-controller HTTP/WS server via route.ReCreateServer)
	// and executor.ApplyConfig (proxies, rules, listeners).
	// executor.ApplyConfig alone does NOT start the API server,
	// which is why yacd couldn't reach 127.0.0.1:9090.
	hub.ApplyConfig(cfg)
	return &mihomoRunner{}, nil
}
