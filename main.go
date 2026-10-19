package main

import (
	"embed"
	"os"
	"slices"

	"zeusvpn/internal/sysproxy"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

const Version = "1.0.0.3"

func main() {
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
	err := wails.Run(&options.App{
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
		Windows: &windows.Options{
			Theme:                windows.Dark,
			WebviewUserDataPath:  webviewDataDir(),
			DisablePinchZoom:     true,
			ZoomFactor:           1.0,
			IsZoomControlEnabled: false,
		},
	})
	if err != nil {
		showError(appTitle + " could not start:\n\n" + err.Error() + "\n\nMake sure Microsoft Edge WebView2 Runtime is installed.")
	}
}
