package main

import (
	"embed"
	"os"
	"slices"

	"zeusvpn/internal/appapi"
	"zeusvpn/internal/sysproxy"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

const Version = appapi.Version

func main() {
	// The Full VPN helper (macOS, Linux) runs without a window.
	if runSpecialMode() {
		return
	}
	// Used by the installer: undo a system proxy left by a killed instance.
	if slices.Contains(os.Args, "--cleanup") {
		sysproxy.Restore()
		return
	}
	elevatedRelaunch := slices.Contains(os.Args, "--elevated")
	if !bindingsMode && !acquireSingleInstance(elevatedRelaunch) {
		focusExistingWindow()
		return
	}
	app := NewApp(slices.Contains(os.Args, "--connect"), slices.Contains(os.Args, "--minimized"))
	opts := &options.App{
		Title:            appTitle,
		Width:            390,
		Height:           720,
		DisableResize:    true,
		Frameless:        true,
		StartHidden:      false,
		BackgroundColour: &options.RGBA{R: 9, G: 11, B: 26, A: 255},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnShutdown:       app.shutdown,
		Bind:             []interface{}{app},
	}
	platformOptions(opts)
	if err := wails.Run(opts); err != nil {
		showError(appTitle + " could not start:\n\n" + err.Error() + startHint)
	}
}
