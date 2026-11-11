package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"zeusvpn/internal/netx"
	"zeusvpn/internal/tunnel"
	"zeusvpn/internal/weblogin"
)

func TestFirefoxRelayProxyGatesSignInAndUsesSOCKS(t *testing.T) {
	target := make(chan string, 1)
	socks, err := tunnel.ListenMixed("127.0.0.1:0", func(_ context.Context, addr string) (net.Conn, error) {
		target <- addr
		return nil, errors.New("relay unavailable")
	}, nil, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer socks.Close()
	relay, err := netx.NewRelay("socks5://"+socks.Addr(), 0)
	if err != nil {
		t.Fatal(err)
	}
	bridge, entry, err := listenRelayLoginProxy(relay)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	proxyURL := &url.URL{Scheme: "http", Host: bridge.Addr()}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: 3 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Get(entry)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != weblogin.PageURL {
		t.Fatalf("unexpected sign-in gate response: %s, %q", resp.Status, resp.Header.Get("Location"))
	}
	if resp, err := client.Get(weblogin.PageURL); err == nil {
		resp.Body.Close()
		t.Fatal("sign-in unexpectedly succeeded despite SOCKS5 failure")
	}
	select {
	case got := <-target:
		if got != "accounts.firefox.com:443" {
			t.Fatalf("SOCKS5 got unexpected sign-in destination: %s", got)
		}
	default:
		t.Fatal("Firefox sign-in did not reach SOCKS5")
	}
}
