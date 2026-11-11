package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"zeusvpn/internal/appapi"
	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/netx"
	"zeusvpn/internal/store"
	"zeusvpn/internal/weblogin"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is bound to the frontend (window.go.main.App); the API itself lives in
// internal/appapi and is shared with the mobile apps.
type App struct {
	*appapi.App
	ctx         context.Context
	autoConnect bool
	minimized   bool
	webLoginMu  sync.Mutex
	webLoginOn  bool
}

// The public Worker URL can be overridden by a release build.
var managedRelayURL = store.ManagedRelayURL

// NewApp must stay side-effect free: `wails build` runs the binary to
// generate bindings, so all real work starts in startup().
func NewApp(autoConnect, minimized bool) *App {
	a := &App{autoConnect: autoConnect, minimized: minimized}
	a.App = appapi.New(appapi.Platform{
		Name:           platformName,
		ManagedRelay:   managedRelayURL,
		Autostart:      autostartEnabled,
		SetAutostart:   setAutostart,
		RestartAsAdmin: restartAsAdminFunc(a),
		OpenURL:        func(url string) { runtime.BrowserOpenURL(a.ctx, url) },
		OpenLogFile:    openLogFile,
	})
	return a
}

func (a *App) SaveSettings(s store.Settings) error {
	a.webLoginMu.Lock()
	defer a.webLoginMu.Unlock()
	cur := appapi.Engine(a.App).Settings()
	if a.webLoginOn && strings.TrimSpace(s.RelayProxy) != cur.RelayProxy {
		return errors.New("finish Firefox sign-in before changing the relay")
	}
	if platformName != "windows" && s.RelayProxy != cur.RelayProxy {
		return errors.New("the connection relay setting is available on Windows")
	}
	return a.App.SaveSettings(s)
}

// StartWebLogin opens Firefox Accounts in the WebView. An injected hook
// sends the session to a loopback receiver before returning to the app UI.
func (a *App) StartWebLogin(returnURL string) error {
	a.webLoginMu.Lock()
	defer a.webLoginMu.Unlock()
	if a.webLoginOn {
		return errors.New("Firefox sign-in is already open")
	}
	relay, err := appapi.Engine(a.App).ConnectionRelay()
	if err != nil {
		return err
	}
	if returnURL == "" {
		returnURL = "http://wails.localhost/index.html"
	}
	wl, err := weblogin.Start()
	if err != nil {
		return err
	}
	a.webLoginOn = true
	execJS := func(js string) {
		if a.ctx != nil {
			runtime.WindowExecJS(a.ctx, js)
		}
	}
	// The shared hook reports results through __zeusDeliver, which on the
	// desktop navigates to the loopback receiver (CSP and Private Network
	// Access do not block top-level navigations). The receiver only answers
	// deliveries carrying this login's nonce; NONCE is the hook's own
	// constant, in scope of this closure.
	deliver := "function(q) { window.location.href = " + jsString(wl.ReceiverURL()+"/") +
		" + q + (q.indexOf('?') !== -1 ? '&' : '?') + 'nonce=' + NONCE; }"
	hook := wl.HookJSWithBrowserExchange(deliver)
	if relay != nil {
		go a.relayWebLogin(wl, relay, hook)
		return nil
	}
	go func() {
		defer a.clearWebLogin()
		navStart := time.Now()
		var lastErr error
		ticker := time.NewTicker(100 * time.Millisecond)
		probe := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		defer probe.Stop()
		defer wl.Stop()
		finished := false
		reachable := false
		reloads := 0
		a.App.Log("info", "web sign-in: opening the Firefox page")
		for !finished {
			select {
			case res := <-wl.Result():
				if res.Unverified {
					reachable = true
					continue
				}
				finished = true
				// Leave the FxA page right away: its own post-login
				// navigation can land on its clientId validation error —
				// the session is already in and the token exchange
				// continues here in the engine.
				execJS(`window.location.replace(` + jsString(returnURL) + `);`)
				lastErr = a.acceptWebLogin(res)
			case <-probe.C:
				// Reachability probe from Go: the webview and this probe use
				// the same network path. Once the hook pings, the page is
				// loaded — a failing probe must then never abort the flow
				// (the user may be mid-sign-in).
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				err := mozilla.ProbeContentServer(ctx)
				cancel()
				reachable = reachable || err == nil
				if reachable {
					if time.Since(navStart) > 4*time.Minute {
						finished = true
						lastErr = errors.New("the sign-in page loaded but no session arrived — try again")
					}
					continue
				}
				// The page never loaded: reload it automatically a few times
				// (flaky networks), then give up with guidance.
				if time.Since(navStart) > time.Duration(15*reloads+15)*time.Second {
					if reloads >= 2 {
						finished = true
						lastErr = errors.New("the Firefox sign-in page did not load on this network — connect any VPN or proxy tool first, or use a different network")
					} else {
						reloads++
						a.App.Log("info", fmt.Sprintf("web sign-in: reloading the page (attempt %d)", reloads+1))
						execJS(`window.location.href = ` + jsString(weblogin.PageURL) + `;`)
					}
				}
			case <-ticker.C:
				// (Re-)inject the hook on every page load.
				execJS(hook)
			}
		}
		if lastErr != nil {
			a.App.Log("warning", "web sign-in: "+lastErr.Error())
		} else {
			a.App.Log("info", "web sign-in: session captured")
		}
		execJS(`window.location.replace(` + jsString(returnURL) + `);`)
		a.App.WebLoginFinished("", lastErr)
	}()
	a.App.Log("info", "web sign-in: navigating the app window to the Firefox page")
	execJS(`window.location.href = ` + jsString(weblogin.PageURL) + `;`)
	return nil
}

func (a *App) clearWebLogin() {
	a.webLoginMu.Lock()
	a.webLoginOn = false
	a.webLoginMu.Unlock()
}

func (a *App) acceptWebLogin(res weblogin.Result) error {
	if res.Err != nil {
		return res.Err
	}
	if res.Tokens != nil {
		return a.App.CompleteWebLoginTokens(res.Email, *res.Tokens)
	}
	return a.App.CompleteWebLogin(res.Email, res.SessionToken)
}

func (a *App) relayWebLogin(wl *weblogin.Login, relay *netx.Relay, hook string) {
	defer wl.Stop()
	ctx, cancel := context.WithTimeout(a.ctx, 4*time.Minute)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runRelayLoginWindow(ctx, relay, hook)
	}()
	err := a.waitRelayLogin(ctx, cancel, wl.Result(), done)
	a.clearWebLogin()
	a.App.WebLoginFinished("", err)
	runtime.EventsEmit(a.ctx, "relay-sign-in-finished", err == nil)
}

func (a *App) waitRelayLogin(ctx context.Context, closeWindow context.CancelFunc, results <-chan weblogin.Result, done <-chan error) error {
	var pending weblogin.Result
	var verified <-chan bool
	var stopCheck context.CancelFunc
	defer func() {
		if stopCheck != nil {
			stopCheck()
		}
	}()
	finish := func(res weblogin.Result) error {
		if stopCheck != nil {
			stopCheck()
		}
		closeWindow()
		<-done
		return a.acceptWebLogin(res)
	}
	for {
		select {
		case res := <-results:
			if !res.Unverified {
				return finish(res)
			}
			if res.SessionToken == pending.SessionToken {
				continue
			}
			if stopCheck != nil {
				stopCheck()
			}
			pending = res
			checkCtx, stop := context.WithCancel(ctx)
			stopCheck = stop
			ready := make(chan bool, 1)
			verified = ready
			go pollFirefoxVerification(checkCtx, res.SessionToken, ready)
		case ok := <-verified:
			verified = nil
			if ok {
				return finish(pending)
			}
		case err := <-done:
			if err == nil {
				return errors.New("Firefox sign-in was closed")
			}
			return err
		case <-ctx.Done():
			<-done
			return errors.New("Firefox sign-in timed out through the relay")
		}
	}
}

func pollFirefoxVerification(ctx context.Context, session string, ready chan<- bool) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		ok, err := mozilla.SessionVerified(ctx, session)
		if ok || errors.Is(err, mozilla.ErrInteractiveChallenge) {
			ready <- ok
			return
		}
		var rejected *mozilla.FxaError
		if errors.As(err, &rejected) && (rejected.Status == 401 || rejected.Status == 403) {
			ready <- false
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	appapi.Startup(a.App, func(event string, data any) { runtime.EventsEmit(ctx, event, data) })
}

func (a *App) domReady(ctx context.Context) {
	if a.minimized {
		runtime.WindowMinimise(ctx)
	}
	appapi.AutoConnect(a.App, a.autoConnect)
}

func (a *App) shutdown(ctx context.Context) {
	appapi.Shutdown(a.App)
	releaseSingleInstance()
}
