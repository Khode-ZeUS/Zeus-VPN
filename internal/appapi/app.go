// Package appapi is the API the UI calls (window.go.main.App). The desktop
// app binds it with Wails; the Android and iOS apps reach it through the
// mobile bindings and a small JavaScript bridge, so every platform shares
// the same UI and behaviour.
package appapi

import (
	"errors"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"zeusvpn/internal/core"
	"zeusvpn/internal/killswitch"
	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/store"
	"zeusvpn/internal/usage"
)

// Platform plugs the host operating system into the API. Nil functions mean
// the feature is not available.
type Platform struct {
	// Name is "windows", "macos", "linux", "android" or "ios".
	Name           string
	Autostart      func() bool
	SetAutostart   func(bool) error
	RestartAsAdmin func() error
	OpenURL        func(string)
	OpenLogFile    func()
	// ListApps enumerates the installed apps for per-app tunnelling
	// (Android); nil where the platform cannot filter by app.
	ListApps func() []AppItem
	// External runs the connection in another process (the iOS network
	// extension), which reports back through SetExternalState.
	External External
}

type External interface {
	Connect() error
	Disconnect()
}

// Features tells the UI which controls apply to this platform.
type Features struct {
	ProxyMode      bool `json:"proxyMode"`
	KillSwitch     bool `json:"killSwitch"`
	Autostart      bool `json:"autostart"`
	LogFile        bool `json:"logFile"`
	WindowControls bool `json:"windowControls"`
	AppFilter      bool `json:"appFilter"`
}

// AppItem is one installed app in the per-app tunnel picker.
type AppItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// App is the API exposed to the frontend. Its exported methods are the
// calls the UI can make; lifecycle functions are package-level (Startup,
// AutoConnect, Shutdown) so they are not exposed.
type App struct {
	eng  *core.Engine
	plat Platform
	emit func(event string, data any)

	extMu    sync.Mutex
	extState *core.State
	extUp    int64
	extDown  int64
}

// New must stay side-effect free: `wails build` runs the binary to generate
// bindings, so all real work starts in Startup.
func New(p Platform) *App {
	return &App{plat: p}
}

type Snapshot struct {
	core.State
	LoggedIn  bool               `json:"loggedIn"`
	Email     string             `json:"email"`
	Accounts  []core.AccountInfo `json:"accounts"`
	Settings  store.Settings     `json:"settings"`
	Elevated  bool               `json:"elevated"`
	Autostart bool               `json:"autostart"`
	Version   string             `json:"version"`
	Platform  string             `json:"platform"`
	Features  Features           `json:"features"`
}

type Stats struct {
	Up       int64 `json:"up"`
	Down     int64 `json:"down"`
	UpRate   int64 `json:"upRate"`
	DownRate int64 `json:"downRate"`
	// Remaining monthly data, counted down live between server refreshes.
	// QuotaLimit is 0 when unknown; the UI falls back to the snapshot then.
	QuotaLimit int64 `json:"quotaLimit"`
	QuotaLeft  int64 `json:"quotaLeft"`
}

type CityDTO struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type CountryDTO struct {
	Code   string    `json:"code"`
	Name   string    `json:"name"`
	Cities []CityDTO `json:"cities"`
}

// Version is the app version shown in the UI (keep in sync with wails.json
// and the mobile projects).
const Version = "1.1.0.8"

// Startup creates the engine; emit delivers events ("state", "stats",
// "log", "notice") to the UI.
func Startup(a *App, emit func(event string, data any)) {
	a.emit = emit
	a.eng = core.NewEngine()
	a.eng.OnState = func(core.State) { a.emitState() }
	a.eng.OnLog = func(line string) { emit("log", line) }
	a.eng.OnNotice = func(msg string) { emit("notice", msg) }
	usage.LoadHosts()
	usage.PruneHosts()
	go a.statsLoop()
	if a.eng.Auth.LoggedIn() {
		go func() {
			a.restoreSessions()
			a.fillMissingEmail()
			a.RefreshQuotas()
		}()
	}
}

// restoreSessions quietly renews every stored account whose token expired,
// so existing users never see the sign-in screen again unless Mozilla
// actually rejected their session.
func (a *App) restoreSessions() {
	for _, acc := range a.eng.Auth.Accounts() {
		if acc.NeedsLogin {
			continue
		}
		if _, err := a.eng.Auth.AccessToken(acc.Email, false); err == nil {
			a.eng.Log("info", "session restored for "+acc.Email)
		}
	}
	a.emitState()
}

// AutoConnect connects on launch when asked to (force) or when the user
// enabled "Connect on launch".
func AutoConnect(a *App, force bool) {
	if (force || a.eng.Settings().AutoConnect) && a.eng.Auth.LoggedIn() {
		go a.Connect()
	}
}

// Shutdown disconnects before the app exits.
func Shutdown(a *App) {
	if a.eng != nil {
		a.disconnect()
	}
	usage.SaveHosts()
}

// Log writes a line into the app log (and the logs view).
func (a *App) Log(level, msg string) { a.eng.Log(level, msg) }

// Engine gives platform code access to the engine.
func Engine(a *App) *core.Engine { return a.eng }

// SetExternalState reports the state of a connection running in another
// process (nil when it stopped), with its traffic counters.
func SetExternalState(a *App, st *core.State, up, down int64) {
	a.extMu.Lock()
	prev := a.extState
	changed := !reflect.DeepEqual(prev, st)
	a.extState, a.extUp, a.extDown = st, up, down
	a.extMu.Unlock()
	if !changed {
		return
	}
	if (prev == nil || prev.Status != core.StatusConnected) && st != nil && st.Status == core.StatusConnected {
		// The extension may have refreshed tokens and quotas.
		a.eng.Auth.Reload()
	}
	a.emitState()
}

func (a *App) state() core.State {
	a.extMu.Lock()
	ext := a.extState
	a.extMu.Unlock()
	if ext != nil {
		return *ext
	}
	if a.plat.External != nil {
		mode := a.eng.Settings().Mode
		return core.State{Status: core.StatusDisconnected, Mode: mode, Account: a.eng.Auth.ActiveEmail()}
	}
	return a.eng.State()
}

func (a *App) traffic() (int64, int64) {
	if a.plat.External != nil {
		a.extMu.Lock()
		defer a.extMu.Unlock()
		return a.extUp, a.extDown
	}
	return a.eng.Traffic()
}

func (a *App) statsLoop() {
	var lastUp, lastDown int64
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for tick := 0; ; tick++ {
		<-t.C
		up, down := a.traffic()
		if up < lastUp || down < lastDown {
			lastUp, lastDown = 0, 0
		}
		limit, left := a.liveQuota()
		a.emit("stats", Stats{Up: up, Down: down, UpRate: up - lastUp, DownRate: down - lastDown, QuotaLimit: limit, QuotaLeft: left})
		lastUp, lastDown = up, down
		// Every 5 minutes while connected, re-read the account's remaining
		// data from Mozilla: other clients of the same account consume it too.
		if tick%300 == 0 && tick > 0 && a.plat.External == nil {
			go a.eng.RefreshActiveQuota()
		}
		if tick%60 == 0 && tick > 0 {
			usage.SaveHosts()
		}
	}
}

// liveQuota is the remaining data of the connection in use, estimated
// between server refreshes. Where the connection runs in another process
// (iOS) the engine knows no counters, so unknown (0) is reported and the
// UI falls back to the snapshot.
func (a *App) liveQuota() (limit, left int64) {
	if a.plat.External != nil || a.eng == nil {
		return 0, 0
	}
	return a.eng.LiveQuota()
}

// fillMissingEmail names an account migrated from an old install.
func (a *App) fillMissingEmail() {
	if a.eng.Auth.ActiveEmail() != "" {
		return
	}
	token, err := a.eng.Auth.AccessToken("", false)
	if err != nil {
		return
	}
	if p, err := mozilla.FetchProfile(token); err == nil && p.Email != "" {
		a.eng.Auth.SetEmail("", p.Email)
		a.emitState()
	}
}

func (a *App) emitState() { a.emit("state", a.GetSnapshot()) }

func (a *App) features() Features {
	desktop := a.plat.External == nil && a.plat.Name != "android"
	return Features{
		ProxyMode:      desktop,
		KillSwitch:     killswitch.Supported,
		Autostart:      a.plat.SetAutostart != nil,
		LogFile:        a.plat.OpenLogFile != nil,
		WindowControls: desktop,
		// Only Android's VPN API can capture individual apps; per-app VPN
		// on iOS needs MDM, and the desktop TUN stack has no per-app filter.
		AppFilter: a.plat.Name == "android",
	}
}

// GetApps lists the installed apps for the per-app tunnel picker (empty
// where unsupported; the Android app answers this call natively before it
// reaches Go).
func (a *App) GetApps() []AppItem {
	if a.plat.ListApps != nil {
		return a.plat.ListApps()
	}
	return []AppItem{}
}

func (a *App) GetSnapshot() Snapshot {
	s := a.eng.Settings()
	autostart := false
	if a.plat.Autostart != nil {
		autostart = a.plat.Autostart()
	}
	return Snapshot{
		State:     a.state(),
		LoggedIn:  a.eng.Auth.LoggedIn(),
		Email:     a.eng.Auth.ActiveEmail(),
		Accounts:  a.eng.Auth.Accounts(),
		Settings:  s,
		Elevated:  !core.TunNeedsElevation || core.IsElevated(),
		Autostart: autostart,
		Version:   Version,
		Platform:  a.plat.Name,
		Features:  a.features(),
	}
}

// CompleteWebLogin stores the account from the web sign-in flow: the FxA
// page handed the session over, and the app exchanged it for tokens.
func (a *App) CompleteWebLogin(email, sessionToken string) error {
	tok, err := mozilla.CompleteWebLogin(sessionToken)
	if err != nil {
		return err
	}
	if email == "" {
		if p, perr := mozilla.FetchProfile(tok.AccessToken); perr == nil {
			email = p.Email
		}
	}
	if err := a.eng.Auth.AddWebAccount(email, tok); err != nil {
		return err
	}
	a.eng.Log("info", "signed in as "+email+" (web sign-in)")
	a.emitState()
	go a.RefreshQuotas()
	return nil
}

// WebLoginFinished reports the outcome of a web sign-in to the UI. The
// webview is already back on the app page by the time this runs.
func (a *App) WebLoginFinished(email string, err error) {
	if err != nil {
		a.eng.Log("warning", "web sign-in failed: "+err.Error())
		a.eng.SetError("Web sign-in failed: " + err.Error())
		return
	}
	a.emitState()
}

func active(st string) bool {
	return st == core.StatusConnected || st == core.StatusReconnecting
}

// RemoveAccount signs one account out. A live connection moves to the next
// account, or disconnects when none is left.
func (a *App) RemoveAccount(email string) error {
	wasActive := a.eng.Auth.ActiveEmail() == email
	a.eng.Auth.Remove(email)
	if !a.eng.Auth.LoggedIn() {
		a.disconnect()
	} else if wasActive && active(a.state().Status) {
		if a.plat.External != nil {
			a.reconnectIfActive()
		} else if err := a.eng.UseAccount(a.eng.Auth.ActiveEmail()); err != nil {
			a.disconnect()
		}
	}
	a.emitState()
	return nil
}

func (a *App) UseAccount(email string) error {
	var err error
	if a.plat.External != nil {
		if err = a.eng.Auth.SetActive(email); err == nil {
			a.reconnectIfActive()
		}
	} else {
		err = a.eng.UseAccount(email)
	}
	a.emitState()
	return err
}

// RefreshQuotas re-reads the remaining data of every account.
func (a *App) RefreshQuotas() {
	a.eng.RefreshQuotas()
	a.emitState()
}

func (a *App) OpenLogFile() {
	if a.plat.OpenLogFile != nil {
		a.plat.OpenLogFile()
	}
}

func (a *App) GetServers(force bool) ([]CountryDTO, error) {
	list, err := a.eng.Servers(force)
	if err != nil {
		return nil, err
	}
	seen := map[string]int{}
	var out []CountryDTO
	for _, c := range list {
		var cities []CityDTO
		for _, city := range c.Cities {
			cities = append(cities, CityDTO{Code: city.Code, Name: city.Name})
		}
		sort.Slice(cities, func(i, j int) bool { return cities[i].Name < cities[j].Name })
		// The list can contain the same country twice; merge them.
		if i, ok := seen[c.Code]; ok {
			out[i].Cities = append(out[i].Cities, cities...)
			continue
		}
		seen[c.Code] = len(out)
		out = append(out, CountryDTO{Code: c.Code, Name: c.Name, Cities: cities})
	}
	return out, nil
}

func (a *App) GetLatency() map[string]int { return a.eng.Latency() }

// UsageEntry is one destination's traffic for the usage panel.
type UsageEntry struct {
	Host string `json:"host"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
	// Last is the unix time (seconds) of the latest traffic.
	Last int64 `json:"last"`
}

// GetUsage lists the destinations the tunnelled data went to, largest
// first (`limit <= 0` returns all). Counted where the tunnel streams
// carry the bytes, so it works in every mode and on every platform.
func (a *App) GetUsage(limit int) []UsageEntry {
	entries := usage.TopHosts(limit)
	out := make([]UsageEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, UsageEntry{Host: e.Host, Up: e.Up, Down: e.Down, Last: e.Last})
	}
	return out
}

// GetAppUsage lists how much data each application sent and received
// through Zeus VPN, heaviest first. Windows only (the per-app lookup needs
// the OS TCP table); an empty list means unsupported or no data yet.
func (a *App) GetAppUsage() []usage.App { return usage.ListApps() }

// ResetUsage forgets the whole usage history (destinations and apps).
func (a *App) ResetUsage() { usage.Reset() }

// reconnectIfActive applies a changed setting to a live connection.
func (a *App) reconnectIfActive() {
	if active(a.state().Status) {
		go func() {
			a.disconnect()
			a.Connect()
		}()
	}
}

func (a *App) SetLocation(country, city string) error {
	s := a.eng.Settings()
	s.CountryCode, s.CityCode = country, city
	if err := a.eng.SaveSettings(s); err != nil {
		return err
	}
	a.reconnectIfActive()
	return nil
}

func (a *App) SetMode(mode string) error {
	if mode != store.ModeTUN {
		mode = store.ModeProxy
	}
	if !a.features().ProxyMode {
		mode = store.ModeTUN
	}
	s := a.eng.Settings()
	if s.Mode == mode {
		return nil
	}
	s.Mode = mode
	if err := a.eng.SaveSettings(s); err != nil {
		return err
	}
	a.emitState()
	if mode == store.ModeTUN && core.TunNeedsElevation && !core.IsElevated() {
		return nil
	}
	a.reconnectIfActive()
	return nil
}

func (a *App) SaveSettings(s store.Settings) error {
	cur := a.eng.Settings()
	s.Email, s.Mode, s.CountryCode, s.CityCode = cur.Email, cur.Mode, cur.CountryCode, cur.CityCode
	if s.ProxyPort < 1024 || s.ProxyPort > 65535 {
		return errors.New("port must be between 1024 and 65535")
	}
	switch s.AppTunnelMode {
	case store.AppTunnelInclude, store.AppTunnelExclude:
		if len(s.Apps) == 0 {
			s.AppTunnelMode = store.AppTunnelAll
			s.Apps = nil
		}
	default:
		s.AppTunnelMode = store.AppTunnelAll
		s.Apps = nil
	}
	appsChanged := s.AppTunnelMode != cur.AppTunnelMode || !slices.Equal(s.Apps, cur.Apps)
	changed := s.ProxyPort != cur.ProxyPort || s.BypassIran != cur.BypassIran || s.BypassLAN != cur.BypassLAN || s.TLSSplit != cur.TLSSplit || appsChanged
	if s.KillSwitch && !cur.KillSwitch && killswitch.Supported && !core.IsElevated() {
		// Saved anyway; the next connect offers to restart as administrator.
		defer a.emit("notice", "The kill switch needs administrator rights. Zeus VPN will ask to restart as administrator when you connect.")
	}
	if err := a.eng.SaveSettings(s); err != nil {
		return err
	}
	if changed {
		a.reconnectIfActive()
	}
	return nil
}

func (a *App) SetAutostart(on bool) error {
	if a.plat.SetAutostart == nil {
		return errors.New("not available on this platform")
	}
	return a.plat.SetAutostart(on)
}

// Elevation errors tell the UI which feature needs administrator rights.
var (
	ErrElevationTUN        = errors.New("ELEVATION_REQUIRED:tun")
	ErrElevationKillSwitch = errors.New("ELEVATION_REQUIRED:killswitch")
)

func (a *App) Connect() error {
	if !a.eng.Auth.LoggedIn() {
		return core.ErrNotLoggedIn
	}
	s := a.eng.Settings()
	if s.Mode != store.ModeTUN && !a.features().ProxyMode {
		// The mobile apps only have Full VPN mode.
		s.Mode = store.ModeTUN
		a.eng.SaveSettings(s)
	}
	if !core.IsElevated() {
		if s.Mode == store.ModeTUN && core.TunNeedsElevation {
			return ErrElevationTUN
		}
		if s.KillSwitch && killswitch.Supported {
			return ErrElevationKillSwitch
		}
	}
	if a.plat.External != nil {
		return a.plat.External.Connect()
	}
	a.eng.Connect()
	return nil
}

func (a *App) disconnect() {
	if a.plat.External != nil {
		a.plat.External.Disconnect()
		return
	}
	a.eng.Disconnect()
}

func (a *App) Disconnect() { a.disconnect() }

// RestartAsAdmin relaunches elevated (for full VPN mode) and connects.
func (a *App) RestartAsAdmin() error {
	if a.plat.RestartAsAdmin == nil {
		return errors.New("not needed on this platform")
	}
	return a.plat.RestartAsAdmin()
}

func (a *App) GetLogs() []string { return a.eng.Logs() }

func (a *App) OpenURL(url string) {
	if a.plat.OpenURL != nil {
		a.plat.OpenURL(url)
	}
}
