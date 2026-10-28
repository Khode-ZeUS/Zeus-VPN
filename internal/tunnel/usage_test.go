//go:build windows

package tunnel

import (
	"context"
	"io"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"zeusvpn/internal/usage"
)

// A connection from ANOTHER process (curl) through a real mixed server
// must be attributed to curl.exe.
func TestMixedServerAttributesExternalProcess(t *testing.T) {
	up, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	go func() {
		for {
			c, err := up.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) { io.Copy(io.Discard, c); c.Close() }(c)
		}
	}()

	dial := func(ctx context.Context, target string) (net.Conn, error) { return net.Dial("tcp", up.Addr().String()) }
	forward := func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1,
			Header: http.Header{"Content-Length": {"2"}}, Body: io.NopCloser(strings.NewReader("hi")),
			ContentLength: 2, Request: r}, nil
	}
	mixed, err := ListenMixed("127.0.0.1:0", dial, forward, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	defer mixed.Close()

	cmd := exec.Command("curl.exe", "-s", "-x", "http://"+mixed.Addr(), "-o", "NUL", "http://example.invalid/test")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("curl: %v: %s", err, out)
	}
	time.Sleep(300 * time.Millisecond)

	found := false
	for _, a := range usage.ListApps() {
		t.Logf("app: %s up=%d down=%d", a.Name, a.Up, a.Down)
		if strings.EqualFold(a.Name, "curl.exe") {
			found = true
		}
	}
	if !found {
		t.Fatal("curl.exe was not attributed")
	}
}
