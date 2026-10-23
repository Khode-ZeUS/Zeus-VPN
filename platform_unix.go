//go:build (darwin && !ios) || (linux && !android)

package main

import (
	"os"
	"path/filepath"
	"syscall"

	"zeusvpn/internal/store"
	"zeusvpn/internal/tunhelper"
)

const appTitle = "Zeus VPN"

var lockFile *os.File

// acquireSingleInstance returns false if another instance holds the lock.
func acquireSingleInstance(wait bool) bool {
	f, err := os.OpenFile(filepath.Join(store.Dir(), "zeusvpn.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return true
	}
	if syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		f.Close()
		return false
	}
	lockFile = f
	return true
}

func releaseSingleInstance() {
	if lockFile != nil {
		lockFile.Close()
		lockFile = nil
	}
}

// focusExistingWindow: macOS brings a running app to the front by itself;
// on Linux the window manager decides.
func focusExistingWindow() {}

// runSpecialMode runs the privileged Full VPN helper when asked to.
func runSpecialMode() bool {
	if len(os.Args) > 1 && os.Args[1] == tunhelper.Flag {
		os.Exit(tunhelper.Run(os.Args[2:]))
	}
	return false
}

// Full VPN mode asks for the administrator password on connect (see
// internal/tunhelper), so the app never restarts itself elevated.
func restartAsAdminFunc(*App) func() error { return nil }
