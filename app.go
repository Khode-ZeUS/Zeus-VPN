package main

import (
	"context"

	"zeusvpn/internal/appapi"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is bound to the frontend (window.go.main.App); the API itself lives in
// internal/appapi and is shared with the mobile apps.
type App struct {
	*appapi.App
	ctx         context.Context
	autoConnect bool
	minimized   bool
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
