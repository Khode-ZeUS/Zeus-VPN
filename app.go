package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"zeusvpn/internal/appapi"
	"zeusvpn/internal/mozilla"
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
	webLogin    *weblogin.Login
}

// NewApp must stay side-effect free: `wails build` runs the binary to
// generate bindings, so all real work starts in startup().
func NewApp(autoConnect, minimized bool) *App {
	a := &App{autoConnect: autoConnect, minimized: minimized}
	a.App = appapi.New(appapi.Platform{
		Name:           platformName,
		Autostart:      autostartEnabled,
		SetAutostart:   setAutostart,
		RestartAsAdmin: restartAsAdminFunc(a),
		OpenURL:        func(url string) { runtime.BrowserOpenURL(a.ctx, url) },
		OpenLogFile:    openLogFile,
	})
	return a
}

// StartWebLogin signs in through the Firefox Accounts web page: the API
// password-login route is edge-blocked (HTTP 406) since late Oct 2026, so
// the app's webview navigates to the sign-in page, an injected hook forwards
// the page's WebChannel session handover to a loopback receiver, and when
// the tokens are in, the webview returns to the app UI. returnURL is the
// app page to come back to (the frontend saves its own location).
// A watchdog returns with an error when the sign-in page cannot load on the
// current network (a VPN connection is then needed for the first sign-in).
func (a *App) StartWebLogin(returnURL string) error {
	if returnURL == "" {
		returnURL = "http://wails.localhost/index.html"
	}
	// A previous flow may still own the webview page: stop it, and accept
	// one in-flight delivery from its page (stale nonce).
	prevNonce := ""
	if a.webLogin != nil {
		prevNonce = a.webLogin.Nonce()
		a.webLogin.Stop()
	}
	wl, err := weblogin.Start()
	if err != nil {
		return err
	}
	wl.SetPreviousNonce(prevNonce)
	wl.SetReturnURL(returnURL)
	a.webLogin = wl
	execJS := func(js string) {
		if a.ctx != nil {
			runtime.WindowExecJS(a.ctx, js)
		}
	}
	// The shared hook reports results through __zeusDeliver, which on the
	// desktop navigates to the loopback receiver (CSP and Private Network
	// Access do not block top-level navigations).
	deliver := "function(q) { window.location.href = " + jsString(wl.ReceiverURL()+"/") +
		" + q + (q.indexOf('?') !== -1 ? '&' : '?') + 'nonce=' + NONCE; }"
	hook := wl.HookJS(deliver, wl.ReceiverURL()+"/", wl.Nonce())
	go func() {
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
				finished = true
				var lerr error
				switch {
				case res.Err != nil:
					lerr = res.Err
				case res.Tokens != nil:
					lerr = a.App.CompleteWebLoginTokens(res.Email, *res.Tokens)
				default:
					lerr = a.App.CompleteWebLogin(res.Email, res.SessionToken)
				}
				lastErr = lerr
			case <-probe.C:
				// Reachability probe from Go: the webview and this probe use
				// the same network path. Once the hook pings, the page is
				// loaded — a failing probe must then never abort the flow
				// (the user may be mid-sign-in).
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				err := mozilla.ProbeContentServer(ctx)
				cancel()
				if err == nil {
					reachable = true
				}
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
			case <-wl.StopChan():
				return
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
