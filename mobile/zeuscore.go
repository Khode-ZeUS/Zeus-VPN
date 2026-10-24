// Package zeuscore is the Go library linked into the Android and iOS apps
// (built with `gomobile bind`). The apps show the same web UI as the desktop
// app; a small JavaScript bridge forwards its calls to Call and receives
// events through Host.OnEvent.
//
// Android runs everything in one process. On iOS the connection runs in the
// network extension (StartTunnel/StopTunnel/TunnelStatus), while the app
// process handles sign-in, settings and servers and shows the extension's
// state, reported with SetExternalState.
package zeuscore

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"zeusvpn/internal/appapi"
	"zeusvpn/internal/core"
	"zeusvpn/internal/store"
	"zeusvpn/internal/vpn"
)

// Host is implemented by the Android/iOS app.
type Host interface {
	// OnEvent delivers a UI event ("state", "stats", "log", "notice") whose
	// payload is JSON.
	OnEvent(name, payload string)
	OpenURL(url string)
	// OpenTun sets up the system VPN interface described by the JSON config
	// (address4, prefix4, address6, prefix6, dns, mtu, bypassLan) and
	// returns its file descriptor, which Go then owns. The iOS extension
	// returns 0: Go finds the descriptor itself.
	OpenTun(config string) (int32, error)
	// CloseTun stops the system VPN interface.
	CloseTun()
}

// TunnelController starts and stops the iOS network extension.
type TunnelController interface {
	StartTunnel() error
	StopTunnel()
}

// tunConfig is the JSON passed to Host.OpenTun.
type tunConfig struct {
	Address4  string `json:"address4"`
	Prefix4   int    `json:"prefix4"`
	Address6  string `json:"address6"`
	Prefix6   int    `json:"prefix6"`
	DNS       string `json:"dns"`
	MTU       int    `json:"mtu"`
	BypassLAN bool   `json:"bypassLan"`
	// TunnelMode ("all", "include", "exclude") and Apps (package names)
	// select which apps the system VPN captures (Android only).
	TunnelMode string   `json:"tunnelMode"`
	Apps       []string `json:"apps"`
	// Protect lists the VPN server's addresses; the host keeps them off the
	// VPN routes so the engine's own connection cannot loop.
	Protect []string `json:"protect"`
}

const mtu = 1500

var (
	mu  sync.Mutex
	app *appapi.App
)

// mobileTun runs Full VPN mode on the descriptor the OS VPN API returns.
type mobileTun struct {
	host Host
	v    *vpn.VPN
	open bool
}

func (t *mobileTun) Prepare() error { return nil }

func (t *mobileTun) Start(cfg core.TunConfig) error {
	c := tunConfig{
		Address4: vpn.Inet4Prefix.Addr().String(), Prefix4: vpn.Inet4Prefix.Bits(),
		Address6: vpn.Inet6Prefix.Addr().String(), Prefix6: vpn.Inet6Prefix.Bits(),
		DNS: vpn.DNSServer.String(), MTU: mtu, BypassLAN: cfg.BypassLAN,
		TunnelMode: cfg.AppTunnelMode, Apps: cfg.Apps,
	}
	if c.TunnelMode == "" {
		c.TunnelMode = "all"
	}
	for _, a := range cfg.Protect {
		c.Protect = append(c.Protect, a.String())
	}
	b, _ := json.Marshal(c)
	fd, err := t.host.OpenTun(string(b))
	if err != nil {
		return fmt.Errorf("could not start the VPN: %w", err)
	}
	t.open = true
	if fd <= 0 {
		fd = int32(findTunFD())
	}
	if fd <= 0 {
		return errors.New("could not start the VPN: no interface")
	}
	v, err := vpn.Start(nil, vpn.Options{FD: int(fd), MTU: mtu, Dial: cfg.Dial, Forward: cfg.Forward, BypassIran: cfg.BypassIran, Protect: cfg.Protect, ProtectPort: cfg.ProtectPort, Log: cfg.Log})
	if err != nil {
		return err
	}
	t.v = v
	return nil
}

func (t *mobileTun) LUID() uint64 { return 0 }

func (t *mobileTun) Close() {
	if t.v != nil {
		t.v.Close()
		t.v = nil
	}
	if t.open {
		t.open = false
		t.host.CloseTun()
	}
}

func emitter(host Host) func(string, any) {
	return func(event string, data any) {
		b, err := json.Marshal(data)
		if err != nil {
			return
		}
		host.OnEvent(event, string(b))
	}
}

// Start creates the engine for the UI. platform is "android" or "ios";
// controller is nil on Android, where the connection runs in this process.
func Start(dataDir, platform string, host Host, controller TunnelController) {
	mu.Lock()
	defer mu.Unlock()
	if app != nil {
		return
	}
	store.SetDir(dataDir)
	core.NewTunDriver = func() core.TunDriver { return &mobileTun{host: host} }
	p := appapi.Platform{
		Name:        platform,
		OpenURL:     host.OpenURL,
		OpenLogFile: nil,
	}
	if controller != nil {
		p.External = external{controller}
	}
	a := appapi.New(p)
	appapi.Startup(a, emitter(host))
	// The mobile apps only have Full VPN mode.
	if e := appapi.Engine(a); e.Settings().Mode != store.ModeTUN {
		s := e.Settings()
		s.Mode = store.ModeTUN
		e.SaveSettings(s)
	}
	app = a
}

type external struct{ c TunnelController }

func (e external) Connect() error { return e.c.StartTunnel() }
func (e external) Disconnect()    { e.c.StopTunnel() }

func current() (*appapi.App, error) {
	mu.Lock()
	defer mu.Unlock()
	if app == nil {
		return nil, errors.New("not started")
	}
	return app, nil
}

// AutoConnect connects if the user enabled "Connect on launch" (or force).
func AutoConnect(force bool) {
	if a, err := current(); err == nil {
		appapi.AutoConnect(a, force)
	}
}

// Shutdown disconnects (Android: when the VPN permission is revoked).
func Shutdown() {
	if a, err := current(); err == nil {
		appapi.Shutdown(a)
	}
}

var errorType = reflect.TypeOf((*error)(nil)).Elem()

// Call invokes an API method (the names window.go.main.App has on the
// desktop) with a JSON array of arguments and returns its JSON result.
func Call(method, args string) (string, error) {
	a, err := current()
	if err != nil {
		return "", err
	}
	m := reflect.ValueOf(a).MethodByName(method)
	if !m.IsValid() {
		return "", fmt.Errorf("unknown method %s", method)
	}
	var raw []json.RawMessage
	if args != "" {
		if err := json.Unmarshal([]byte(args), &raw); err != nil {
			return "", fmt.Errorf("%s: bad arguments: %w", method, err)
		}
	}
	t := m.Type()
	if len(raw) != t.NumIn() {
		return "", fmt.Errorf("%s takes %d arguments, got %d", method, t.NumIn(), len(raw))
	}
	in := make([]reflect.Value, len(raw))
	for i, r := range raw {
		v := reflect.New(t.In(i))
		if err := json.Unmarshal(r, v.Interface()); err != nil {
			return "", fmt.Errorf("%s: argument %d: %w", method, i+1, err)
		}
		in[i] = v.Elem()
	}
	out := m.Call(in)
	var result any
	for _, o := range out {
		if o.Type() == errorType {
			if !o.IsNil() {
				return "", o.Interface().(error)
			}
			continue
		}
		result = o.Interface()
	}
	b, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// tunnelState is what the iOS extension reports to the app.
type tunnelState struct {
	State core.State `json:"state"`
	Up    int64      `json:"up"`
	Down  int64      `json:"down"`
}

// SetExternalState reports the iOS extension's TunnelStatus JSON to the UI;
// an empty string means the tunnel is not running.
func SetExternalState(status string) {
	a, err := current()
	if err != nil {
		return
	}
	if status == "" {
		appapi.SetExternalState(a, nil, 0, 0)
		return
	}
	var ts tunnelState
	if json.Unmarshal([]byte(status), &ts) != nil {
		return
	}
	appapi.SetExternalState(a, &ts.State, ts.Up, ts.Down)
}

// ---------- iOS network extension ----------

var (
	tunMu  sync.Mutex
	tunEng *core.Engine
)

// StartTunnel connects inside the iOS network extension, using the
// accounts and settings the app saved in the shared dataDir. It returns
// once connected, or with the reason the connection failed.
func StartTunnel(dataDir string, host Host) error {
	tunMu.Lock()
	if tunEng != nil {
		tunMu.Unlock()
		return errors.New("already running")
	}
	store.SetDir(dataDir)
	core.NewTunDriver = func() core.TunDriver { return &mobileTun{host: host} }
	e := core.NewEngine()
	s := e.Settings()
	if s.Mode != store.ModeTUN {
		s.Mode = store.ModeTUN
		e.SaveSettings(s)
	}
	done := make(chan core.State, 8)
	e.OnState = func(st core.State) {
		if st.Status == core.StatusConnected || st.Status == core.StatusDisconnected {
			select {
			case done <- st:
			default:
			}
		}
		host.OnEvent("state", "")
	}
	e.OnLog = func(line string) { host.OnEvent("log", line) }
	e.OnNotice = func(msg string) { host.OnEvent("notice", msg) }
	tunEng = e
	tunMu.Unlock()

	e.Connect()
	select {
	case st := <-done:
		if st.Status == core.StatusConnected {
			return nil
		}
		StopTunnel()
		if st.Error == "" {
			st.Error = "disconnected"
		}
		return errors.New(st.Error)
	case <-time.After(90 * time.Second):
		StopTunnel()
		return errors.New("timed out connecting")
	}
}

// StopTunnel disconnects the extension's engine.
func StopTunnel() {
	tunMu.Lock()
	e := tunEng
	tunEng = nil
	tunMu.Unlock()
	if e != nil {
		e.Disconnect()
	}
}

// TunnelStatus returns the extension's state as JSON ({"state", "up", "down"}).
func TunnelStatus() string {
	tunMu.Lock()
	e := tunEng
	tunMu.Unlock()
	if e == nil {
		return ""
	}
	up, down := e.Traffic()
	b, _ := json.Marshal(tunnelState{State: e.State(), Up: up, Down: down})
	return string(b)
}
