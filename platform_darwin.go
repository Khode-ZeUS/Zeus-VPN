package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"zeusvpn/internal/core"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

const (
	platformName = "macos"
	startHint    = ""
	launchAgent  = "com.zeusvpn.app"
)

func platformOptions(o *options.App) {
	o.Mac = &mac.Options{
		TitleBar:             mac.TitleBarHidden(),
		Appearance:           mac.NSAppearanceNameDarkAqua,
		WebviewIsTransparent: false,
		About:                &mac.AboutInfo{Title: appTitle, Message: "Mozilla VPN client · " + Version},
	}
}

func showError(msg string) {
	esc := strings.NewReplacer(`\`, `\`, `"`, `\"`).Replace
	exec.Command("/usr/bin/osascript", "-e", `display alert "`+esc(appTitle)+`" message "`+esc(msg)+`" as critical`).Run()
}

func openLogFile() {
	exec.Command("/usr/bin/open", "-R", core.LogPath()).Start()
}

func launchAgentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", launchAgent+".plist")
}

func autostartEnabled() bool {
	_, err := os.Stat(launchAgentPath())
	return err == nil
}

// setAutostart adds a LaunchAgent that starts the app minimized at login.
func setAutostart(on bool) error {
	p := launchAgentPath()
	if !on {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>--minimized</string></array>
	<key>RunAtLoad</key><true/>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, launchAgent, esc(exe))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(plist), 0o644)
}
