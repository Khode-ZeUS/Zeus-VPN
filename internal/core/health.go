package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"zeusvpn/internal/killswitch"
	"zeusvpn/internal/mozilla"
)

const (
	probeTarget     = "www.cloudflare.com:443"
	probeInterval   = 10 * time.Second
	retryInterval   = 3 * time.Second
	failsBeforeDrop = 3
	// After this many failed probes while reconnecting, try another server.
	failsBeforeFailover = 6
)

// probe checks that the tunnel can still open a connection.
func (e *Engine) probe(ctx context.Context) error {
	e.mu.Lock()
	up := e.up
	e.mu.Unlock()
	if up == nil {
		return errors.New("no tunnel")
	}
	pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := up.Dial(pctx, probeTarget)
	if err != nil {
		return err
	}
	c.Close()
	return nil
}

// healthLoop notices when the tunnel stops working, reports it as
// "reconnecting" (engaging the kill switch if enabled) and restores the
// connected state once the tunnel works again.
func (e *Engine) healthLoop(ctx context.Context) {
	fails := 0
	down := false
	for {
		wait := probeInterval
		if fails > 0 {
			wait = retryInterval
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		err := e.probe(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			if down {
				e.restored()
			}
			fails, down = 0, false
			continue
		}
		fails++
		if !down && fails >= failsBeforeDrop {
			down = true
			e.dropped(err)
		}
		if down && fails%failsBeforeFailover == 0 {
			e.failover(ctx)
		}
	}
}

func (e *Engine) dropped(cause error) {
	e.Log("warning", "connection to the VPN server lost: "+cause.Error())
	e.update(func(s *State) { s.Status = StatusReconnecting })
	st := e.Settings()
	if !st.KillSwitch || !killswitch.Supported {
		e.notify("VPN connection lost, reconnecting…")
		return
	}
	e.mu.Lock()
	var luid uint64
	if e.tun != nil {
		luid = e.tun.LUID()
	}
	e.mu.Unlock()
	if err := killswitch.Enable(luid, st.BypassLAN); err != nil {
		e.Log("error", "kill switch could not block traffic: "+err.Error())
		e.notify("VPN connection lost, and the kill switch failed: " + err.Error())
		return
	}
	e.Log("info", "kill switch on: internet blocked until the VPN reconnects or you disconnect")
	e.notify("VPN connection lost. Kill switch is blocking the internet until it reconnects.")
}

func (e *Engine) restored() {
	if killswitch.Active() {
		killswitch.Disable()
		e.Log("info", "kill switch off: internet unblocked")
	}
	e.update(func(s *State) {
		s.Status = StatusConnected
		s.Error = ""
	})
	e.Log("info", "connection to the VPN server restored")
	e.notify("VPN connection restored")
}

// failover moves the tunnel to another server of the chosen location.
func (e *Engine) failover(ctx context.Context) {
	e.mu.Lock()
	up := e.up
	e.mu.Unlock()
	if up == nil {
		return
	}
	countries, err := e.Servers(false)
	if err != nil {
		return
	}
	st := e.Settings()
	current := up.Host()
	var ep mozilla.Endpoint
	for i := 0; i < 5; i++ {
		ep, err = mozilla.PickEndpoint(countries, st.CountryCode, st.CityCode)
		if err != nil || ep.Host != current {
			break
		}
	}
	if err != nil || ep.Host == current {
		return
	}
	if err := up.Retarget(ctx, ep.Host, ep.Port); err != nil {
		e.Log("warning", "could not switch server: "+err.Error())
		return
	}
	// Keep the loop protection pointed at the new server.
	e.mu.Lock()
	tun := e.tun
	e.mu.Unlock()
	if tun != nil {
		if rp, ok := tun.(RouteProtector); ok {
			rp.UpdateProtected(up.Addrs(), ep.Port)
		}
	}
	e.Log("info", fmt.Sprintf("trying another server: %s (%s)", ep.Addr(), ep.CityName))
	e.update(func(s *State) {
		s.Server = &ServerInfo{CountryCode: ep.CountryCode, CountryName: ep.CountryName, CityName: ep.CityName, Host: ep.Host}
	})
}
