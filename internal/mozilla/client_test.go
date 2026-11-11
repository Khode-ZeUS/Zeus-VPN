package mozilla

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeRT struct {
	calls int
	err   error
	body  []string
}

func (f *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls++
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		f.body = append(f.body, string(b))
	}
	if f.err != nil {
		return nil, f.err
	}
	return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
}

func TestViaTunnelFallsBackOnDirectFailure(t *testing.T) {
	direct := &fakeRT{err: errors.New("dial tcp 151.101.1.91:443: i/o timeout")}
	tun := &fakeRT{}
	rt := viaTunnel{direct: direct, tunnel: func() http.RoundTripper { return tun }}
	req, _ := http.NewRequest("POST", "https://vpn.mozilla.org/api/v1/fpn/token", strings.NewReader(`{"a":1}`))
	resp, err := rt.RoundTrip(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("expected fallback success, got %v", err)
	}
	if direct.calls != 1 || tun.calls != 1 {
		t.Fatalf("calls direct=%d tunnel=%d", direct.calls, tun.calls)
	}
	if tun.body[0] != `{"a":1}` {
		t.Fatalf("body not replayed: %q", tun.body[0])
	}
}

func TestRequiredRelayDoesNotFallback(t *testing.T) {
	offline := errors.New("relay unavailable")
	direct, vpn, system := &fakeRT{}, &fakeRT{}, &fakeRT{}
	relay := &fakeRT{err: offline}
	rt := viaTunnel{
		direct:      direct,
		relay:       func() http.RoundTripper { return relay },
		tunnel:      func() http.RoundTripper { return vpn },
		systemProxy: func() http.RoundTripper { return system },
	}
	req, _ := http.NewRequest("POST", "https://api.accounts.firefox.com/v1/account/login", strings.NewReader(`{}`))
	if _, err := rt.RoundTrip(req); !errors.Is(err, offline) {
		t.Fatalf("sign-in request returned %v, want relay error", err)
	}
	if relay.calls != 1 || direct.calls+vpn.calls+system.calls != 0 {
		t.Fatalf("calls: relay=%d direct=%d vpn=%d system=%d", relay.calls, direct.calls, vpn.calls, system.calls)
	}
}

func TestViaTunnelWithoutTunnelReturnsError(t *testing.T) {
	direct := &fakeRT{err: errors.New("timeout")}
	rt := viaTunnel{direct: direct, tunnel: func() http.RoundTripper { return nil }}
	req, _ := http.NewRequest("GET", "https://vpn.mozilla.org/", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("expected the direct error")
	}
}

func TestViaTunnelSkipsTunnelOnSuccess(t *testing.T) {
	direct := &fakeRT{}
	tun := &fakeRT{}
	rt := viaTunnel{direct: direct, tunnel: func() http.RoundTripper { return tun }}
	req, _ := http.NewRequest("GET", "https://vpn.mozilla.org/", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if tun.calls != 0 {
		t.Fatal("tunnel used although direct worked")
	}
}

// A bare 406 from the direct path must be retried through the tunnel: the
// edge block is per-IP, and the tunnel's exit may not be blocked.
func TestViaTunnelRetries406ThroughTunnel(t *testing.T) {
	tunnelHits := 0
	tun := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		tunnelHits++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"access_token":"t"}`)), Header: make(http.Header)}, nil
	})
	direct := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 406, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})
	rt := viaTunnel{direct: direct, tunnel: func() http.RoundTripper { return tun }, systemProxy: func() http.RoundTripper { return nil }}
	req, _ := http.NewRequest("POST", "https://api.accounts.firefox.com/v1/account/login", strings.NewReader(`{}`))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected the tunnel's 200, got %d", resp.StatusCode)
	}
	if tunnelHits != 1 {
		t.Fatalf("tunnel used %d times, want 1", tunnelHits)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
