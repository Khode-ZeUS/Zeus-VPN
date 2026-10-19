//go:build e2e

package core

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"testing"
	"time"

	"zeusvpn/internal/killswitch"
)

// curlOK reports whether another program can reach the internet directly.
func curlOK() bool {
	out, _ := exec.Command("curl.exe", "-s", "--noproxy", "*", "--max-time", "8", "-o", "NUL", "-w", "%{http_code}", "https://www.digikala.com/").Output()
	code := strings.TrimSpace(string(out))
	return code != "" && code != "000"
}

func waitStatus(e *Engine, want string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if e.State().Status == want {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// Live, elevated test: drop detection, kill switch, failover and restore.
// Build with: go test -c -tags e2e -o ks.test.exe ./internal/core
func TestKillSwitch(t *testing.T) {
	if !IsElevated() {
		t.Skip("needs administrator rights")
	}
	e := NewEngine()
	e.OnLog = func(l string) { t.Log(l) }
	orig := e.Settings()
	s := orig
	s.Mode, s.KillSwitch, s.ProxyPort = "proxy", true, 10829
	e.SaveSettings(s)
	defer e.SaveSettings(orig)

	t.Logf("before connect: other apps online = %v", curlOK())
	e.Connect()
	if !waitStatus(e, StatusConnected, 40*time.Second) {
		t.Fatalf("did not connect: %+v", e.State())
	}
	t.Logf("connected via %s; other apps online = %v", e.State().Server.Host, curlOK())

	// Simulate the server going away.
	e.mu.Lock()
	up := e.up
	e.mu.Unlock()
	if err := up.Retarget(context.Background(), "192.0.2.1", 443); err != nil {
		t.Fatal(err)
	}
	if !waitStatus(e, StatusReconnecting, 90*time.Second) {
		t.Fatalf("drop not detected: %+v", e.State())
	}
	blocked := !curlOK()
	t.Logf("drop detected; kill switch active = %v; other apps blocked = %v", killswitch.Active(), blocked)
	if !killswitch.Active() || !blocked {
		t.Error("kill switch did not block traffic")
	}
	if c, err := net.DialTimeout("tcp", "www.digikala.com:443", 8*time.Second); err != nil {
		t.Errorf("kill switch blocked Zeus VPN itself: %v", err)
	} else {
		c.Close()
		t.Log("Zeus VPN itself can still reach the network")
	}

	if !waitStatus(e, StatusConnected, 3*time.Minute) {
		t.Fatalf("did not recover: %+v", e.State())
	}
	t.Logf("recovered via %s; kill switch active = %v; other apps online = %v", e.State().Server.Host, killswitch.Active(), curlOK())
	if killswitch.Active() {
		t.Error("kill switch still active after recovery")
	}
	e.Disconnect()
	t.Logf("disconnected; other apps online = %v", curlOK())
}
