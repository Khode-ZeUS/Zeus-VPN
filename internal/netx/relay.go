package netx

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"zeusvpn/internal/store"

	"golang.org/x/net/proxy"
)

// Relay is the required first hop. The relay resolves the destination.
type Relay struct {
	ip          netip.Addr
	port        int
	scheme      string
	dial        func(context.Context, string, string) (net.Conn, error)
	workerHost  string
	managed     bool
	fxaEndpoint url.URL
	workerRoute atomic.Pointer[workerRoute]
}

type workerRoute struct {
	addrs []netip.Addr
	dial  func(context.Context, string, string) (net.Conn, error)
}

func NewRelay(raw string, localProxyPort int) (*Relay, error) {
	if raw == "" {
		return nil, nil
	}
	invalid := errors.New("enter a wss:// Worker URL or a SOCKS5 relay with an IP and port (no credentials)")
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, invalid
	}
	if u.Scheme == "wss" {
		if u.Hostname() == "" || (u.Port() != "" && u.Port() != "443") {
			return nil, invalid
		}
		r := &Relay{scheme: "wss", workerHost: u.Hostname()}
		dialer := workerWebSocketDialer
		dialer.NetDialContext = r.dialWorkerEdge
		endpoint := u.String()
		token := os.Getenv("ZEUS_WORKER_PILOT_TOKEN")
		if token == "" {
			r.dial = workerDialer(*u, dialer, store.LoadWorkerPilotToken(endpoint), false)
			return r, nil
		}
		dial := workerDialer(*u, dialer, token, false)
		var save sync.Once
		r.dial = func(ctx context.Context, network, target string) (net.Conn, error) {
			conn, err := dial(ctx, network, target)
			if err == nil {
				save.Do(func() { _ = store.SaveWorkerPilotToken(endpoint, token) })
			}
			return conn, err
		}
		return r, nil
	}
	if u.Scheme != "socks5" || u.Path != "" {
		return nil, invalid
	}
	host, p, err := net.SplitHostPort(u.Host)
	if err != nil {
		return nil, invalid
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() && !ip.IsLoopback() {
		return nil, invalid
	}
	ip = ip.Unmap()
	port, err := strconv.ParseUint(p, 10, 16)
	if err != nil || port == 0 {
		return nil, invalid
	}
	if ip.IsLoopback() && int(port) == localProxyPort {
		return nil, errors.New("the relay must be a separate proxy; use a different port from Zeus VPN's local proxy")
	}
	d, err := proxy.SOCKS5("tcp", net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), nil, relayNetwork{})
	if err != nil {
		return nil, err
	}
	return &Relay{ip: ip, port: int(port), scheme: "socks5", dial: d.(proxy.ContextDialer).DialContext}, nil
}

// NewManagedRelay uses the operator-selected public endpoint. It never reads
// or sends credentials configured for a personal Worker.
func NewManagedRelay(raw string) (*Relay, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "wss" || u.Hostname() == "" ||
		(u.Port() != "" && u.Port() != "443") || u.Path != "/managed" ||
		u.RawPath != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("the built-in relay address is invalid")
	}
	r := &Relay{scheme: "wss", workerHost: u.Hostname(), managed: true}
	r.fxaEndpoint = url.URL{Scheme: "https", Host: u.Host, Path: "/managed/fxa"}
	dialer := workerWebSocketDialer
	dialer.NetDialContext = r.dialWorkerEdge
	r.dial = workerDialer(*u, dialer, "", true)
	return r, nil
}

func (r *Relay) WorkerHost() string {
	if r == nil {
		return ""
	}
	return r.workerHost
}

// TunnelRelay preserves explicit custom relays. The built-in Worker handles
// Mozilla API traffic, while VPN data goes directly to the VPN server.
func (r *Relay) TunnelRelay() *Relay {
	if r != nil && r.managed {
		return nil
	}
	return r
}

func (r *Relay) FirefoxAccountsTransport() *http.Transport {
	if r == nil || !r.managed {
		return nil
	}
	return &http.Transport{
		DialContext: r.dialWorkerEdge, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 10 * time.Second, IdleConnTimeout: 30 * time.Second,
	}
}

func (r *Relay) FirefoxAccountsEndpoint() url.URL {
	if r == nil {
		return url.URL{}
	}
	return r.fxaEndpoint
}

// BindWorker pins DNS before the TUN starts and uses sockets bound to the
// physical interface. CDN addresses can also serve unrelated websites, so
// excluding those addresses from the TUN would bypass it for those sites.
func (r *Relay) BindWorker(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error)) error {
	addrs, err := LookupHost(ctx, r.workerHost)
	if err != nil {
		return errors.New("could not resolve the Worker relay")
	}
	if len(addrs) == 0 {
		return errors.New("the Worker relay has no addresses")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.workerRoute.Store(&workerRoute{addrs: addrs, dial: dial})
	return nil
}

func (r *Relay) UnbindWorker() {
	if r != nil {
		r.workerRoute.Store(nil)
	}
}

func (r *Relay) dialWorkerEdge(ctx context.Context, network, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if route := r.workerRoute.Load(); route != nil {
		return dialResolvedConn(ctx, network, port, route.addrs, route.dial)
	}
	addrs, err := LookupHost(ctx, r.workerHost)
	if err != nil || len(addrs) == 0 {
		return nil, errors.New("could not resolve the Worker relay")
	}
	return dialResolvedConn(ctx, network, port, addrs, DialContext)
}

func (r *Relay) Route() ([]netip.Addr, int) {
	if !r.ip.IsValid() || r.ip.IsLoopback() {
		return nil, 0
	}
	return []netip.Addr{r.ip}, r.port
}

func (r *Relay) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, err := r.dial(ctx, network, addr)
	if err != nil {
		return nil, fmt.Errorf("%s relay: %w", r.scheme, err)
	}
	return c, nil
}

type relayNetwork struct{}

func (r relayNetwork) Dial(network, addr string) (net.Conn, error) {
	return r.DialContext(context.Background(), network, addr)
}

func (relayNetwork) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, _ := net.SplitHostPort(addr)
	if ip, err := netip.ParseAddr(host); err == nil && ip.IsLoopback() {
		// Do not bind a loopback relay socket to the physical interface.
		return (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext(ctx, network, addr)
	}
	return DialContext(ctx, network, addr)
}
