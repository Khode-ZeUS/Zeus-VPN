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
	"zeusvpn/internal/sysproxy"

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
		rt := viaTunnel{
			direct: retryOnBlock{transport},
			tunnel: func() http.RoundTripper {
				if t := tunnelTransport.Load(); t != nil {
					return t
				}
				return nil
			},
			systemProxy: systemProxyTransport,
		}
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

// replayableFresh is replayable with a fresh 30 s context: a retry after the
// client timeout must not inherit the already-expired deadline, or it fails
// before the connection is even opened.
func replayableFresh(req *http.Request) (*http.Request, bool) {
	if req.Body == nil || req.Body == http.NoBody {
		r := req.Clone(context.Background())
		return r, true
	}
	if req.GetBody == nil {
		return nil, false
	}
	body, err := req.GetBody()
	if err != nil {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	r := req.Clone(ctx)
	r.Body = body
	_ = cancel // the request owns the deadline from here on
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
// once more through the VPN tunnel when one is available, and — last resort,
// e.g. for the first sign-in on a filtered network — through whatever proxy
// the operating system has configured (another VPN tool the user runs).
type viaTunnel struct {
	direct      http.RoundTripper
	tunnel      func() http.RoundTripper
	systemProxy func() http.RoundTripper
}

func (v viaTunnel) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := v.direct.RoundTrip(req)
	retryable := false
	switch {
	case err != nil:
		// Transport failure (timeout, reset): the network path is broken.
		retryable = !errors.Is(err, context.Canceled)
	case resp != nil && resp.StatusCode == http.StatusNotAcceptable:
		// The edge refused this client on the direct path (bare 406). A
		// foreign exit — the tunnel — may not be refused, so give it a
		// chance before surfacing the block.
		retryable = true
	}
	if !retryable {
		return resp, err
	}
	if resp != nil && resp.Body != nil {
		resp.Body.Close() // replaced by the retry
	}
	// A fresh context for every retry: the direct attempt may have burned
	// the client timeout, and each fallback gets its own.
	if t := v.tunnel(); t != nil {
		if again, ok := replayableFresh(req); ok {
			if resp2, err2 := t.RoundTrip(again); err2 == nil && resp2.StatusCode != http.StatusNotAcceptable {
				return resp2, nil
			} else if resp2 != nil {
				resp2.Body.Close()
			}
		}
	}
	if v.systemProxy != nil {
		if sp := v.systemProxy(); sp != nil {
			if again, ok := replayableFresh(req); ok {
				if resp2, err2 := sp.RoundTrip(again); err2 == nil && resp2.StatusCode != http.StatusNotAcceptable {
					return resp2, nil
				} else if resp2 != nil {
					resp2.Body.Close()
				}
			}
		}
	}
	return resp, err
}

// systemProxyTransport builds a round tripper through the OS-configured
// proxy, or returns nil when none is set. Rebuilt at most every 10 seconds.
var (
	sysProxyMu      sync.Mutex
	sysProxyRT      http.RoundTripper
	sysProxyChecked time.Time
)

func systemProxyTransport() http.RoundTripper {
	sysProxyMu.Lock()
	defer sysProxyMu.Unlock()
	if time.Since(sysProxyChecked) < 10*time.Second {
		return sysProxyRT
	}
	sysProxyChecked = time.Now()
	sysProxyRT = nil
	if server, ok := sysproxy.SystemProxy(); ok {
		u, err := url.Parse("http://" + server)
		if err == nil {
			sysProxyRT = &http.Transport{
				Proxy:               http.ProxyURL(u),
				DialContext:         netx.DialResolved,
				ForceAttemptHTTP2:   true,
				TLSHandshakeTimeout: 15 * time.Second,
				IdleConnTimeout:     60 * time.Second,
			}
		}
	}
	return sysProxyRT
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
