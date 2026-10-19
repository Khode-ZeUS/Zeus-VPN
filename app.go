package main

import (
	"context"
	"errors"
	"os/exec"
	"sort"
	"time"

	"zeusvpn/internal/core"
	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/store"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the API exposed to the frontend (window.go.main.App).
type App struct {
	ctx         context.Context
	eng         *core.Engine
	autoConnect bool
	minimized   bool
}

// NewApp must stay side-effect free: `wails build` runs the binary to
// generate bindings, so all real work starts in startup().
func NewApp(autoConnect, minimized bool) *App {
	return &App{autoConnect: autoConnect, minimized: minimized}
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
}

type Stats struct {
	Up       int64 `json:"up"`
	Down     int64 `json:"down"`
	UpRate   int64 `json:"upRate"`
	DownRate int64 `json:"downRate"`
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

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.eng = core.NewEngine()
	a.eng.OnState = func(core.State) { runtime.EventsEmit(ctx, "state", a.GetSnapshot()) }
	a.eng.OnLog = func(line string) { runtime.EventsEmit(ctx, "log", line) }
	a.eng.OnNotice = func(msg string) { runtime.EventsEmit(ctx, "notice", msg) }
	go a.statsLoop()
	if a.eng.Auth.LoggedIn() {
		go func() {
			a.fillMissingEmail()
			a.RefreshQuotas()
		}()
	}
}

func (a *App) domReady(ctx context.Context) {
	if a.minimized {
		runtime.WindowMinimise(ctx)
	}
	if (a.autoConnect || a.eng.Settings().AutoConnect) && a.eng.Auth.LoggedIn() {
		go a.Connect()
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.eng.Disconnect()
	releaseSingleInstance()
}

func (a *App) statsLoop() {
	var lastUp, lastDown int64
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		up, down := a.eng.Traffic()
		if up < lastUp || down < lastDown {
			lastUp, lastDown = 0, 0
		}
		runtime.EventsEmit(a.ctx, "stats", Stats{Up: up, Down: down, UpRate: up - lastUp, DownRate: down - lastDown})
		lastUp, lastDown = up, down
	}
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

func (a *App) emitState() { runtime.EventsEmit(a.ctx, "state", a.GetSnapshot()) }

func (a *App) GetSnapshot() Snapshot {
	s := a.eng.Settings()
	return Snapshot{
		State:     a.eng.State(),
		LoggedIn:  a.eng.Auth.LoggedIn(),
		Email:     a.eng.Auth.ActiveEmail(),
		Accounts:  a.eng.Auth.Accounts(),
		Settings:  s,
		Elevated:  core.IsElevated(),
		Autostart: autostartEnabled(),
		Version:   Version,
	}
}

func (a *App) afterLogin(step *core.LoginStep, err error) (*core.LoginStep, error) {
	if err != nil {
		return nil, err
	}
	if step.Step == "done" {
		a.emitState()
		go a.RefreshQuotas()
	}
	return step, nil
}

func (a *App) Login(email, password string) (*core.LoginStep, error) {
	return a.afterLogin(a.eng.Auth.Login(email, password))
}

func (a *App) VerifyCode(code string) (*core.LoginStep, error) {
	return a.afterLogin(a.eng.Auth.VerifyCode(code))
}

func (a *App) ConfirmLink() (*core.LoginStep, error) {
	return a.afterLogin(a.eng.Auth.ConfirmLink())
}

func (a *App) SubmitUnblock(code string) (*core.LoginStep, error) {
	return a.afterLogin(a.eng.Auth.SubmitUnblock(code))
}

func (a *App) CancelLogin() { a.eng.Auth.CancelLogin() }

// RemoveAccount signs one account out. A live connection moves to the next
// account, or disconnects when none is left.
func (a *App) RemoveAccount(email string) error {
	wasActive := a.eng.Auth.ActiveEmail() == email
	a.eng.Auth.Remove(email)
	if !a.eng.Auth.LoggedIn() {
		a.eng.Disconnect()
	} else if st := a.eng.State().Status; wasActive && (st == core.StatusConnected || st == core.StatusReconnecting) {
		if err := a.eng.UseAccount(a.eng.Auth.ActiveEmail()); err != nil {
			a.eng.Disconnect()
		}
	}
	a.emitState()
	return nil
}

func (a *App) UseAccount(email string) error {
	err := a.eng.UseAccount(email)
	a.emitState()
	return err
}

// RefreshQuotas re-reads the remaining data of every account.
func (a *App) RefreshQuotas() {
	a.eng.RefreshQuotas()
	a.emitState()
}

func (a *App) OpenLogFile() {
	exec.Command("explorer.exe", "/select,", core.LogPath()).Start()
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

// reconnectIfActive applies a changed setting to a live connection.
func (a *App) reconnectIfActive() {
	if st := a.eng.State().Status; st == core.StatusConnected || st == core.StatusReconnecting {
		go func() {
			a.eng.Disconnect()
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
	s := a.eng.Settings()
	if s.Mode == mode {
		return nil
	}
	s.Mode = mode
	if err := a.eng.SaveSettings(s); err != nil {
		return err
	}
	runtime.EventsEmit(a.ctx, "state", a.GetSnapshot())
	if mode == store.ModeTUN && !core.IsElevated() {
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
	changed := s.ProxyPort != cur.ProxyPort || s.BypassIran != cur.BypassIran || s.BypassLAN != cur.BypassLAN || s.TLSSplit != cur.TLSSplit
	if s.KillSwitch && !cur.KillSwitch && !core.IsElevated() {
		// Saved anyway; the next connect offers to restart as administrator.
		defer runtime.EventsEmit(a.ctx, "notice", "The kill switch needs administrator rights. Zeus VPN will ask to restart as administrator when you connect.")
	}
	if err := a.eng.SaveSettings(s); err != nil {
		return err
	}
	if changed {
		a.reconnectIfActive()
	}
	return nil
}

func (a *App) SetAutostart(on bool) error { return setAutostart(on) }

// Elevation errors tell the UI which feature needs administrator rights.
var (
	ErrElevationTUN        = errors.New("ELEVATION_REQUIRED:tun")
	ErrElevationKillSwitch = errors.New("ELEVATION_REQUIRED:killswitch")
)

func (a *App) Connect() error {
	if !a.eng.Auth.LoggedIn() {
		return core.ErrNotLoggedIn
	}
	if !core.IsElevated() {
		s := a.eng.Settings()
		if s.Mode == store.ModeTUN {
			return ErrElevationTUN
		}
		if s.KillSwitch {
			return ErrElevationKillSwitch
		}
	}
	a.eng.Connect()
	return nil
}

func (a *App) Disconnect() { a.eng.Disconnect() }

// RestartAsAdmin relaunches elevated (for full VPN mode) and connects.
func (a *App) RestartAsAdmin() error {
	if err := relaunchElevated("--connect"); err != nil {
		return errors.New("administrator permission was not granted")
	}
	a.eng.Disconnect()
	releaseSingleInstance()
	runtime.Quit(a.ctx)
	return nil
}

func (a *App) GetLogs() []string { return a.eng.Logs() }

func (a *App) OpenURL(url string) { runtime.BrowserOpenURL(a.ctx, url) }
