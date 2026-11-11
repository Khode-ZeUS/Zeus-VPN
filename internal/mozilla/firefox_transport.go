package mozilla

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

	"zeusvpn/internal/netx"
)

type firefoxTransport struct {
	h1 *http.Transport
	h2 *http2.Transport
}

func newFirefoxTransport(dial func(context.Context, string, string) (net.Conn, error)) firefoxTransport {
	h1 := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return dialFirefox(ctx, dial, network, addr, "http/1.1")
	}
	h2 := func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
		return dialFirefox(ctx, dial, network, addr, "h2")
	}
	return firefoxTransport{
		h1: &http.Transport{DialTLSContext: h1, IdleConnTimeout: 60 * time.Second},
		h2: &http2.Transport{DialTLSContext: h2, IdleConnTimeout: 60 * time.Second},
	}
}

func (t firefoxTransport) closeIdle() {
	t.h1.CloseIdleConnections()
	t.h2.CloseIdleConnections()
}

func dialFirefox(ctx context.Context, dial func(context.Context, string, string) (net.Conn, error), network, addr, protocol string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, err := dial(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	conn := utls.UClient(raw, &utls.Config{ServerName: host, MinVersion: tls.VersionTLS12}, utls.HelloCustom)
	if err := firefoxHandshake(ctx, conn, protocol); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

func firefoxHandshake(ctx context.Context, conn *utls.UConn, protocol string) error {
	spec, err := utls.UTLSIdToSpec(utls.HelloFirefox_Auto)
	if err != nil {
		return err
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{protocol}
		}
	}
	if err := conn.ApplyPreset(&spec); err != nil {
		return err
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		return err
	}
	if protocol == "h2" && conn.ConnectionState().NegotiatedProtocol != protocol {
		return fmt.Errorf("Firefox Accounts did not negotiate HTTP/2")
	}
	return nil
}

var directFirefox = newFirefoxTransport(netx.DialResolved)

type firefoxConnectError struct{ error }

func (e firefoxConnectError) Unwrap() error { return e.error }

var firefoxSignInDirect = &http.Transport{
	DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		conn, err := dialFirefox(ctx, netx.DialResolved, network, addr, "http/1.1")
		if err != nil {
			return nil, firefoxConnectError{err}
		}
		return conn, nil
	},
	IdleConnTimeout: 30 * time.Second,
}

// Keep verification pages and cookies on the same route as the response
// that started the current Firefox Accounts operation.
type firefoxSignInRoute struct {
	direct, relay http.RoundTripper
	selected      atomic.Int32
}

type signInClientKey struct{}
type signInClientState struct {
	once   sync.Once
	client *http.Client
}

func (r *firefoxSignInRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	switch r.selected.Load() {
	case 1:
		return r.direct.RoundTrip(req)
	case 2:
		return r.relay.RoundTrip(req)
	}
	// A RoundTripper may close Body even when connecting fails.
	var retry *http.Request
	if req.Body == nil || req.Body == http.NoBody || req.GetBody != nil {
		retry = req.Clone(req.Context())
	}
	resp, err := r.direct.RoundTrip(req)
	if err == nil {
		r.selected.Store(1)
		return resp, nil
	}
	var beforeRequest firefoxConnectError
	if retry == nil || req.Context().Err() != nil || !errors.As(err, &beforeRequest) {
		return resp, err
	}
	if req.GetBody != nil {
		retry.Body, err = req.GetBody()
		if err != nil {
			return nil, err
		}
	}
	resp, err = r.relay.RoundTrip(retry)
	if err == nil {
		r.selected.Store(2)
	}
	return resp, err
}

func firefoxAccountsClient(ctx context.Context) *http.Client {
	if state, ok := ctx.Value(signInClientKey{}).(*signInClientState); ok {
		state.once.Do(func() { state.client = newFirefoxAccountsClient(ctx) })
		return state.client
	}
	return newFirefoxAccountsClient(ctx)
}

func newFirefoxAccountsClient(ctx context.Context) *http.Client {
	c := *Client()
	if relay := firefoxHTTPRelay.Load(); relay != nil {
		c.Transport = relay
		if ctx.Value(captchaPromptKey{}) != nil {
			c.Transport = &firefoxSignInRoute{direct: firefoxSignInDirect, relay: relay}
		}
	} else if relay := relayTransport.Load(); relay != nil {
		c.Transport = relay.firefox.h1
	} else if route, ok := c.Transport.(viaTunnel); ok {
		route.direct = retryOnBlock{directFirefox.h1}
		c.Transport = route
	}
	return &c
}

func firefoxAccountsHTTP2(c *http.Client) *http.Client {
	h2 := *c
	if relay := relayTransport.Load(); relay != nil {
		if c.Transport != relay.firefox.h1 {
			return nil
		}
		h2.Transport = relay.firefox.h2
	} else if _, ok := c.Transport.(viaTunnel); ok {
		h2.Transport = directFirefox.h2
	} else {
		return nil
	}
	h2.Timeout = 8 * time.Second
	return &h2
}
