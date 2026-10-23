package core

import (
	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/tunnel"
	"zeusvpn/internal/vpn"
)

// TunDriver runs Full VPN mode on one platform.
type TunDriver interface {
	// Prepare runs before the tunnel to the VPN server is opened, so the
	// app's own connections can be kept off the virtual adapter.
	Prepare() error
	// Start begins capturing the system's traffic.
	Start(cfg TunConfig) error
	// LUID identifies the adapter for the Windows kill switch (0 if none).
	LUID() uint64
	Close()
}

type TunConfig struct {
	// Dial opens a connection through the VPN server.
	Dial    tunnel.DialFunc
	Forward tunnel.RoundTripFunc
	// ProxyAddr is the local HTTP + SOCKS5 proxy, for drivers that run the
	// adapter in another process.
	ProxyAddr  string
	BypassIran bool
	BypassLAN  bool
	Log        func(string)
	// OnFail reports that the adapter went away while connected.
	OnFail func(error)
}

// NewTunDriver creates the driver for Full VPN mode. The mobile apps replace
// it with one that uses the operating system's VPN API.
var NewTunDriver = defaultTunDriver

// localTun runs the adapter inside this process, which needs administrator
// (Windows) or root rights.
type localTun struct {
	binding *vpn.Binding
	vpn     *vpn.VPN
}

func (t *localTun) Prepare() error {
	b, err := vpn.NewBinding()
	if err != nil {
		return err
	}
	t.binding = b
	mozilla.ResetConnections()
	return nil
}

func (t *localTun) Start(cfg TunConfig) error {
	v, err := vpn.Start(t.binding, vpn.Options{Dial: cfg.Dial, Forward: cfg.Forward, BypassIran: cfg.BypassIran, ExcludeLAN: cfg.BypassLAN, Log: cfg.Log})
	if err != nil {
		return err
	}
	t.vpn = v
	return nil
}

func (t *localTun) LUID() uint64 {
	if t.vpn == nil {
		return 0
	}
	return t.vpn.LUID()
}

func (t *localTun) Close() {
	if t.vpn != nil {
		t.vpn.Close()
		t.vpn = nil
	}
	if t.binding != nil {
		t.binding.Close()
		t.binding = nil
		mozilla.ResetConnections()
	}
}
