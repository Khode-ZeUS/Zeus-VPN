package netx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"time"

	"golang.org/x/net/proxy"
)

// UpstreamDialer turns a user-supplied proxy URL into a dial function that
// reaches the Internet through it: "socks5://[user:pass@]host:port" or
// "http(s)://[user:pass@]host:port". The connections still leave through
// this process's dialer, so fragmentation and interface binding apply.
// An empty url returns (nil, nil).
func UpstreamDialer(raw string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	if raw == "" {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("bad proxy url: %w", err)
	}
	switch u.Scheme {
	case "socks5":
		var auth *proxy.Auth
		if u.User != nil {
			pw, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pw}
		}
		d, err := proxy.SOCKS5("tcp", u.Host, auth, &netDialer{})
		if err != nil {
			return nil, err
		}
		cd, ok := d.(proxy.ContextDialer)
		if !ok {
			return func(ctx context.Context, network, addr string) (net.Conn, error) { return d.Dial(network, addr) }, nil
		}
		return cd.DialContext, nil
	case "http", "https":
		base, _ := url.Parse("http://d") // placeholder scheme; only the proxy part is used
		base.Scheme = u.Scheme
		base.Host = u.Host
		base.User = u.User
		tr := &http.Transport{
			Proxy:               http.ProxyURL(base),
			DialContext:         DialContext,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 15 * time.Second,
		}
		return func(ctx context.Context, network, addr string) (net.Conn, error) {
			return tr.DialContext(ctx, network, addr)
		}, nil
	}
	return nil, fmt.Errorf("unsupported proxy scheme %q (socks5 or http)", u.Scheme)
}

// netDialer adapts this package's dialer to the proxy package's Dialer.
type netDialer struct{}

func (netDialer) Dial(network, addr string) (net.Conn, error) {
	return DialContext(context.Background(), network, addr)
}

// NetworkSignature identifies the current network by this device's local
// addresses, so per-network choices (like the bootstrap path) can be
// remembered without platform APIs. Best effort: "" when it cannot tell.
func NetworkSignature() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var addrs []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		list, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range list {
			ip := a.String()
			if net.ParseIP(ip) != nil && !ipIsLocal(ip) {
				addrs = append(addrs, ip)
			}
		}
	}
	if len(addrs) == 0 {
		return ""
	}
	sort.Strings(addrs)
	sum := sha256.Sum256([]byte(fmt.Sprint(addrs)))
	return hex.EncodeToString(sum[:])[:16]
}

func ipIsLocal(s string) bool {
	ip := net.ParseIP(s)
	return ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}
