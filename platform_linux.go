package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"zeusvpn/internal/core"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
)

const (
	platformName = "linux"
	startHint    = "\n\nMake sure WebKitGTK (libwebkit2gtk-4.1) is installed."
)

func platformOptions(o *options.App) {
	o.Linux = &linux.Options{
		ProgramName:      "zeusvpn",
		WebviewGpuPolicy: linux.WebviewGpuPolicyOnDemand,
	}
}

func showError(msg string) {
	for _, cmd := range [][]string{
		{"zenity", "--error", "--title", appTitle, "--text", msg},
		{"kdialog", "--title", appTitle, "--error", msg},
	} {
		if _, err := exec.LookPath(cmd[0]); err == nil && exec.Command(cmd[0], cmd[1:]...).Run() == nil {
			return
		}
	}
	fmt.Fprintln(os.Stderr, msg)
}

func openLogFile() {
	exec.Command("xdg-open", filepath.Dir(core.LogPath())).Start()
}

func autostartPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "autostart", "zeusvpn.desktop")
}

func autostartEnabled() bool {
	_, err := os.Stat(autostartPath())
	return err == nil
}

// setAutostart adds an XDG autostart entry that starts the app minimized.
func setAutostart(on bool) error {
	p := autostartPath()
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
	// Exec quoting: escape for the quoted argument, then for the key's string.
	quoted := `"` + strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", "$", `\\$`).Replace(exe) + `"`
	entry := "[Desktop Entry]\nType=Application\nName=Zeus VPN\nExec=" + quoted + " --minimized\nIcon=zeusvpn\nX-GNOME-Autostart-enabled=true\n"
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(entry), 0o644)
}
