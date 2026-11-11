package tunnel

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"zeusvpn/internal/netx"
)

func TestRequiredRelayForWarmupAndRetarget(t *testing.T) {
	targets := make(chan string, 2)
	proxy, err := ListenMixed("127.0.0.1:0", func(_ context.Context, addr string) (net.Conn, error) {
		targets <- addr
		return nil, errors.New("relay refused destination")
	}, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(proxy.Close)
	relay, err := netx.NewRelay("socks5://"+proxy.Addr(), 10820)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	up, err := NewRelayedUpstream(ctx, "first.invalid", 2499, "", nil, relay)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(up.Close)
	for _, host := range []string{"first.invalid", "second.invalid"} {
		if host != "first.invalid" {
			if err := up.Retarget(ctx, host, 2499); err != nil {
				t.Fatal(err)
			}
		}
		if err := up.Warmup(ctx); err == nil {
			t.Fatal("expected relay failure")
		}
		select {
		case got := <-targets:
			if want := host + ":2499"; got != want {
				t.Fatalf("relay got %q, want %q", got, want)
			}
		case <-ctx.Done():
			t.Fatalf("relay was not used for %s", host)
		}
	}
}
