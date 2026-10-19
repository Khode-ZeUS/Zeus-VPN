//go:build e2e

package core

import (
	"context"
	"testing"

	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/netx"
	"zeusvpn/internal/tunnel"
)

// Live test: control plane and tunnel with forced TLS fragmentation.
func TestForcedSplit(t *testing.T) {
	for _, mode := range []string{netx.SplitSNI, netx.SplitFirstByte} {
		netx.SetSplitMode(mode)
		mozilla.ResetConnections()
		e := NewEngine()
		pass, _, err := e.acquirePass(false)
		if err != nil {
			t.Fatalf("%s: control plane: %v", mode, err)
		}
		countries, err := mozilla.FetchServerList()
		if err != nil {
			t.Fatalf("%s: server list: %v", mode, err)
		}
		ep, _ := mozilla.PickEndpoint(countries, "DE", "")
		up, err := tunnel.NewUpstream(context.Background(), ep.Host, ep.Port, pass.Token, nil)
		if err != nil {
			t.Fatal(err)
		}
		c, err := up.Dial(context.Background(), "www.cloudflare.com:443")
		if err != nil {
			t.Fatalf("%s: tunnel: %v", mode, err)
		}
		c.Close()
		up.Close()
		t.Logf("%s: control plane + tunnel to %s ok", mode, ep.Host)
	}
}
