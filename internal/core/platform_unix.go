//go:build (darwin && !ios) || (linux && !android)

package core

import (
	"errors"
	"os"

	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/tunhelper"
	"zeusvpn/internal/vpn"
)

// TunNeedsElevation: on macOS and Linux the app stays unprivileged; a
// small helper asks for the administrator password when Full VPN starts.
const TunNeedsElevation = false

func IsElevated() bool { return os.Geteuid() == 0 }

func defaultTunDriver() TunDriver {
	if IsElevated() {
		return &localTun{}
	}
	return &helperTun{}
}

// helperTun runs the adapter in a privileged helper process that sends all
// traffic to this app's local proxy.
type helperTun struct {
	binding *vpn.Binding
	helper  *tunhelper.Helper
}

func (t *helperTun) Prepare() error {
	// Our own connections (to Mozilla and the VPN server) must bypass the
	// helper's routes.
	b, err := vpn.NewBinding()
	if err != nil {
		return err
	}
	t.binding = b
	mozilla.ResetConnections()
	return nil
}

func (t *helperTun) Start(cfg TunConfig) error {
	onExit := func() { cfg.OnFail(errors.New("the Full VPN helper stopped unexpectedly")) }
	h, err := tunhelper.Launch(tunhelper.Config{
		ProxyAddr: cfg.ProxyAddr, BypassLAN: cfg.BypassLAN,
		Protect: cfg.Protect, ProtectPort: cfg.ProtectPort, OnExit: onExit,
	}, cfg.Log)
	if err != nil {
		return err
	}
	t.helper = h
	return nil
}

func (t *helperTun) LUID() uint64 { return 0 }

func (t *helperTun) Close() {
	if t.helper != nil {
		t.helper.Stop()
		t.helper = nil
	}
	if t.binding != nil {
		t.binding.Close()
		t.binding = nil
		mozilla.ResetConnections()
	}
}
