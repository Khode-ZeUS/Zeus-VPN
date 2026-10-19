//go:build e2e

package core

import (
	"context"
	"testing"
	"time"

	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/tunnel"
)

// Live test against Mozilla: renewal at half-life, then recovery from an
// expired pass. Run with: go test -tags e2e -run TestRenewal -v -timeout 20m ./internal/core
func TestRenewal(t *testing.T) {
	e := NewEngine()
	e.OnLog = func(l string) { t.Log(l) }
	pass, email, err := e.acquirePass(true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("account %s, pass expires in %s", email, time.Until(pass.Expires).Round(time.Second))
	countries, err := e.Servers(false)
	if err != nil {
		t.Fatal(err)
	}
	ep, _ := mozilla.PickEndpoint(countries, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	up, err := tunnel.NewUpstream(ctx, ep.Host, ep.Port, pass.Token, e.onRejected)
	if err != nil {
		t.Fatal(err)
	}
	e.up = up
	e.done = make(chan struct{})
	e.applyPass(pass, email)
	go e.renewLoop(ctx)

	dial := func(label string) {
		c, err := up.Dial(ctx, "www.cloudflare.com:443")
		if err != nil {
			t.Errorf("%s: %v", label, err)
			return
		}
		c.Close()
		t.Logf("%s: ok", label)
	}
	dial("fresh")
	old := pass.Token
	time.Sleep(time.Until(pass.Expires) + 20*time.Second)
	dial("after renewal")
	up.SetToken(old) // simulate a renewal that never happened
	dial("with expired pass")
}
