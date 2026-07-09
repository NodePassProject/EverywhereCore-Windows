package main

import (
	"context"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
	"github.com/sagernet/sing/service/pause"
)

type singBoxRunner struct {
	box          *box.Box
	pauseManager pause.Manager
}

func (s *singBoxRunner) stop() error {
	if s.box != nil {
		return s.box.Close()
	}
	return nil
}

func (s *singBoxRunner) suspend() {
	if s.pauseManager != nil && !s.pauseManager.IsDevicePaused() {
		s.pauseManager.DevicePause()
	}
}

func (s *singBoxRunner) resume() {
	if s.pauseManager != nil && s.pauseManager.IsDevicePaused() {
		s.pauseManager.DeviceWake()
	}
}

// startSingBox boots sing-box exactly like its standalone Windows
// binary would: no adapter.PlatformInterface is installed, so the tun
// inbound opens its own WinTUN adapter (sing-tun ships the dll
// embedded), applies auto_route via winipcfg, and watches the default
// route natively. That is why — unlike the iOS/Android siblings —
// there is no platform struct and no external interface monitor in
// this file.
func startSingBox(configContent string) (coreRunner, error) {
	// include.Context attaches the built-in inbound/outbound/endpoint/
	// DNS-transport/service registries to the context. Without it
	// box.New cannot resolve types declared in the JSON (socks,
	// direct, vmess, …) and start fails immediately.
	ctx := include.Context(context.Background())

	// box.New runs pause.WithDefaultManager on its own copy of the
	// context, so the manager it installs is unreachable from out here.
	// Install one on our ctx up front and box.New will reuse it
	// (WithDefaultManager early-returns when a manager is already
	// registered), which lets us hand DevicePause/DeviceWake to the
	// service's power-event handling.
	ctx = pause.WithDefaultManager(ctx)
	pauseManager := service.FromContext[pause.Manager](ctx)

	options, err := json.UnmarshalExtendedContext[option.Options](ctx, []byte(configContent))
	if err != nil {
		return nil, err
	}
	b, err := box.New(box.Options{
		Context: ctx,
		Options: options,
	})
	if err != nil {
		return nil, err
	}
	if err := b.Start(); err != nil {
		_ = b.Close()
		return nil, err
	}
	return &singBoxRunner{box: b, pauseManager: pauseManager}, nil
}
