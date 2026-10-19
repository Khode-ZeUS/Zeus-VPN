// Package mozilla implements the Mozilla VPN / Firefox VPN control plane:
// Firefox Accounts login, Guardian proxy passes, and the server list.
package mozilla

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zeusvpn/internal/netx"

	"golang.org/x/net/publicsuffix"
)

const (
	UserAgent        = "MozillaVPN/2.35.0 (sys:linux; iap:true)"
	GuardianEndpoint = "https://vpn.mozilla.org"
)

var (
	clientOnce sync.Once
	httpClient *http.Client
	transport  *http.Transport
)

// Client returns the shared control-plane HTTP client. It never honours the
// HTTP(S)_PROXY environment variables: another VPN client's local proxy must
// not sit in front of Mozilla's endpoints.
func Client() *http.Client {
	clientOnce.Do(func() {
		transport = &http.Transport{
			Proxy:               nil,
			DialContext:         netx.DialResolved,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 15 * time.Second,
			IdleConnTimeout:     60 * time.Second,
			MaxIdleConnsPerHost: 4,
		}
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		rt := viaTunnel{direct: retryOnBlock{transport}, tunnel: func() http.RoundTripper {
			if t := tunnelTransport.Load(); t != nil {
				return t
			}
			return nil
		}}
		httpClient = &http.Client{Transport: rt, Jar: jar, Timeout: 30 * time.Second}
	})
	return httpClient
}

// tunnelTransport reaches Mozilla's endpoints through the VPN itself; set
// while connected.
var tunnelTransport atomic.Pointer[http.Transport]

// SetTunnel lets control-plane requests fall back to the VPN tunnel when the
// direct path fails (Mozilla's API hosts are intermittently filtered). Pass
// nil to stop.
func SetTunnel(dial func(ctx context.Context, addr string) (net.Conn, error)) {
	var t *http.Transport
	if dial != nil {
		t = &http.Transport{
			Proxy:               nil,
			DialContext:         func(ctx context.Context, _, addr string) (net.Conn, error) { return dial(ctx, addr) },
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: 15 * time.Second,
			IdleConnTimeout:     60 * time.Second,
		}
	}
	if old := tunnelTransport.Swap(t); old != nil {
		old.CloseIdleConnections()
	}
}

// replayable returns a copy of req that can be sent again, if its body allows.
func replayable(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		return req, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	r := req.Clone(req.Context())
	r.Body = body
	return r, true
}

// retryOnBlock repeats a request once when the network cut off its TLS
// handshake: the retry is sent fragmented (see netx auto mode). The request
// never reached the server, so retrying is safe even for POSTs.
type retryOnBlock struct{ rt http.RoundTripper }

func (r retryOnBlock) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	wasBlocked := netx.HostBlocked(host)
	resp, err := r.rt.RoundTrip(req)
	if err == nil || wasBlocked || !netx.HostBlocked(host) || req.Context().Err() != nil {
		return resp, err
	}
	again, ok := replayable(req)
	if !ok {
		return resp, err
	}
	return r.rt.RoundTrip(again)
}

// viaTunnel sends a request that failed on the direct path (timeout, reset)
// once more through the VPN tunnel, when one is available.
type viaTunnel struct {
	direct http.RoundTripper
	tunnel func() http.RoundTripper
}

func (v viaTunnel) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := v.direct.RoundTrip(req)
	if err == nil || req.Context().Err() != nil {
		return resp, err
	}
	t := v.tunnel()
	if t == nil {
		return resp, err
	}
	again, ok := replayable(req)
	if !ok {
		return resp, err
	}
	return t.RoundTrip(again)
}

// ResetConnections drops pooled connections, e.g. after the outbound
// interface binding changes.
func ResetConnections() {
	Client()
	transport.CloseIdleConnections()
}

func applyHeaders(h http.Header) {
	if h.Get("User-Agent") == "" {
		h.Set("User-Agent", UserAgent)
	}
	if h.Get("Accept") == "" {
		h.Set("Accept", "application/json")
	}
}

func readBody(resp *http.Response, limit int64) []byte {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, limit))
	return b
}

func errorSnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// HTTPError is a non-2xx response from a control-plane endpoint.
type HTTPError struct {
	Status int
	Msg    string
}

func (e *HTTPError) Error() string { return e.Msg }

var (
	ErrTokenInvalid  = errors.New("access token was rejected by Guardian")
	ErrQuotaExceeded = errors.New("account proxy quota exceeded")
)

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(fmt.Sprintf("bad url %q: %v", s, err))
	}
	return u
}
