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
