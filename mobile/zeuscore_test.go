package zeuscore

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

type fakeHost struct {
	mu     sync.Mutex
	events []string
}

func (h *fakeHost) OnEvent(name, payload string) {
	h.mu.Lock()
	h.events = append(h.events, name)
	h.mu.Unlock()
}
func (h *fakeHost) OpenURL(string)                {}
func (h *fakeHost) OpenTun(string) (int32, error) { return 0, nil }
func (h *fakeHost) CloseTun()                     {}

// TestCall checks the JSON dispatch the mobile bridge relies on.
func TestCall(t *testing.T) {
	// The engine keeps its log file open, so t.TempDir's cleanup would fail
	// on Windows. HOME is redirected too: the store migrates a legacy
	// ~/.foxyvpn-tokens.json, and the test must not pick up a real one.
	dir, err := os.MkdirTemp("", "zeuscore-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	Start(dir, "android", &fakeHost{}, nil)

	var snap struct {
		Platform string `json:"platform"`
		LoggedIn bool   `json:"loggedIn"`
		Settings struct {
			Mode    string `json:"mode"`
			Country string `json:"country_code"`
		} `json:"settings"`
		Features struct {
			ProxyMode bool `json:"proxyMode"`
		} `json:"features"`
	}
	get := func() {
		t.Helper()
		out, err := Call("GetSnapshot", "[]")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(out), &snap); err != nil {
			t.Fatal(err)
		}
	}
	get()
	if snap.Platform != "android" || snap.LoggedIn || snap.Settings.Mode != "tun" || snap.Features.ProxyMode {
		t.Fatalf("unexpected snapshot %+v", snap)
	}

	if _, err := Call("SetLocation", `["DE","fra"]`); err != nil {
		t.Fatal(err)
	}
	get()
	if snap.Settings.Country != "DE" {
		t.Fatalf("location not saved: %+v", snap)
	}

	if _, err := Call("Connect", "[]"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("Connect without an account: %v", err)
	}
	if _, err := Call("NoSuchMethod", "[]"); err == nil {
		t.Fatal("unknown method accepted")
	}
	if _, err := Call("SetLocation", `["DE"]`); err == nil {
		t.Fatal("wrong argument count accepted")
	}
}
