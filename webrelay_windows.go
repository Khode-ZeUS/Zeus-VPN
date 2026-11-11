package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"zeusvpn/internal/netx"
	"zeusvpn/internal/store"
	"zeusvpn/internal/tunnel"
	"zeusvpn/internal/weblogin"

	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

const relayLoginHelperArg = "--relay-login-helper"

type relayLoginWindow struct {
	Proxy, Entry, Profile, Hook string
}

// listenRelayLoginProxy lets the browser reach the Firefox page only after
// it has reached the local proxy. Without the proxy, the .invalid host
// cannot resolve and no sign-in page opens.
func listenRelayLoginProxy(relay *netx.Relay) (*tunnel.MixedServer, string, error) {
	gate := strings.ToLower(rand.Text()) + ".invalid"
	p, err := tunnel.ListenMixed("127.0.0.1:0", func(ctx context.Context, addr string) (net.Conn, error) {
		return relay.DialContext(ctx, "tcp", addr)
	}, func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Host != gate || r.URL.Path != "/" {
			return nil, errors.New("the sign-in relay only forwards encrypted connections")
		}
		return &http.Response{
			StatusCode: http.StatusFound,
			ProtoMajor: 1,
			ProtoMinor: 1,
			Header:     http.Header{"Location": {weblogin.PageURL}, "Cache-Control": {"no-store"}},
			Body:       http.NoBody,
		}, nil
	}, func(string) {})
	return p, "http://" + gate + "/", err
}

func runRelayLoginWindow(ctx context.Context, relay *netx.Relay, hook string) error {
	p, entry, err := listenRelayLoginProxy(relay)
	if err != nil {
		return err
	}
	defer p.Close()
	profile, err := os.MkdirTemp(store.Dir(), "webview-relay-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profile)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, exe, relayLoginHelperArg)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	if err := cmd.Start(); err != nil {
		return err
	}
	cfg := relayLoginWindow{Proxy: p.Addr(), Entry: entry, Profile: profile, Hook: hook}
	if err := json.NewEncoder(in).Encode(cfg); err != nil {
		in.Close()
		cmd.Wait()
		return err
	}
	if err := cmd.Wait(); err != nil {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return fmt.Errorf("could not open the Firefox sign-in window through the relay: %s", detail)
		}
		return fmt.Errorf("could not open the Firefox sign-in window through the relay: %w", err)
	}
	return nil
}

var (
	procRelayCreateWindow  = user32.NewProc("CreateWindowExW")
	procRelayDestroyWindow = user32.NewProc("DestroyWindow")
	procRelayIsWindow      = user32.NewProc("IsWindow")
	procRelayGetMessage    = user32.NewProc("GetMessageW")
	procRelayTranslate     = user32.NewProc("TranslateMessage")
	procRelayDispatch      = user32.NewProc("DispatchMessageW")
)

// runRelayLoginHelper hosts a separate WebView so it can pass proxy flags
// through WebView2's environment options. The Wails loader clears externally
// supplied browser flags. This process owns no account or VPN state.
func runRelayLoginHelper() error {
	var cfg relayLoginWindow
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 256<<10)).Decode(&cfg); err != nil {
		return err
	}
	// Stop if the parent process exits while this window is open.
	go func() {
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}()

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := windows.CoInitializeEx(0, 2); err != nil && !errors.Is(err, syscall.Errno(windows.S_FALSE)) {
		return fmt.Errorf("initialize WebView2 thread: %w", err)
	}
	defer windows.CoUninitialize()

	class, _ := windows.UTF16PtrFromString("STATIC")
	title, _ := windows.UTF16PtrFromString("Zeus VPN — Firefox sign-in")
	const style = 0x00C80000 // WS_CAPTION | WS_SYSMENU
	const useDefault = 0x80000000
	var module windows.Handle
	if err := windows.GetModuleHandleEx(windows.GET_MODULE_HANDLE_EX_FLAG_UNCHANGED_REFCOUNT, nil, &module); err != nil {
		return err
	}
	hwnd, _, err := procRelayCreateWindow.Call(0, uintptr(unsafe.Pointer(class)),
		uintptr(unsafe.Pointer(title)), style, useDefault, useDefault, 520, 680, 0, 0, uintptr(module), 0)
	if hwnd == 0 {
		return fmt.Errorf("create Firefox sign-in window: %w", err)
	}
	defer procRelayDestroyWindow.Call(hwnd)

	browser := edge.NewChromium()
	browser.DataPath = cfg.Profile
	browser.AdditionalBrowserArgs = []string{
		"--proxy-server=http://" + cfg.Proxy,
		"--proxy-bypass-list=127.0.0.1",
		"--disable-quic",
		"--disable-features=HttpsUpgrades",
		"--force-webrtc-ip-handling-policy=disable_non_proxied_udp",
		`--host-resolver-rules="MAP * ~NOTFOUND, EXCLUDE 127.0.0.1"`,
	}
	browser.SetErrorCallback(func(err error) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	})
	browser.Embed(hwnd)
	browser.NavigationCompletedCallback = func(*edge.ICoreWebView2, *edge.ICoreWebView2NavigationCompletedEventArgs) {
		browser.Eval(cfg.Hook)
	}
	browser.Init(cfg.Hook)
	browser.Resize()
	procShowWindow.Call(hwnd, windows.SW_SHOWNORMAL)
	if err := browser.Show(); err != nil {
		return err
	}
	browser.Navigate(cfg.Entry)

	var msg struct {
		hwnd           uintptr
		message        uint32
		wParam, lParam uintptr
		time           uint32
		x, y           int32
		private        uint32
	}
	for {
		ret, _, err := procRelayGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		switch int32(ret) {
		case -1:
			return err
		case 0:
			return nil
		}
		procRelayTranslate.Call(uintptr(unsafe.Pointer(&msg)))
		procRelayDispatch.Call(uintptr(unsafe.Pointer(&msg)))
		if alive, _, _ := procRelayIsWindow.Call(hwnd); alive == 0 {
			return nil
		}
	}
}
