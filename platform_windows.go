package main

import (
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"zeusvpn/internal/store"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	mutexName  = "Local\\ZeusVPN-SingleInstance"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "ZeusVPN"
	appTitle   = "Zeus VPN"
)

var (
	user32                  = windows.NewLazySystemDLL("user32.dll")
	procFindWindow          = user32.NewProc("FindWindowW")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procMessageBox          = user32.NewProc("MessageBoxW")
	instanceMutex           windows.Handle
)

// acquireSingleInstance returns false if another instance is running. An
// elevated relaunch waits for the non-elevated instance to exit first.
func acquireSingleInstance(wait bool) bool {
	name, _ := windows.UTF16PtrFromString(mutexName)
	deadline := time.Now().Add(10 * time.Second)
	for {
		h, err := windows.CreateMutex(nil, false, name)
		if err == nil {
			instanceMutex = h
			return true
		}
		if h != 0 {
			windows.CloseHandle(h)
		}
		if !wait || time.Now().After(deadline) {
			return false
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func releaseSingleInstance() {
	if instanceMutex != 0 {
		windows.CloseHandle(instanceMutex)
		instanceMutex = 0
	}
}

func focusExistingWindow() {
	title, _ := windows.UTF16PtrFromString(appTitle)
	hwnd, _, _ := procFindWindow.Call(0, uintptr(unsafe.Pointer(title)))
	if hwnd != 0 {
		procShowWindow.Call(hwnd, 9) // SW_RESTORE
		procSetForegroundWindow.Call(hwnd)
	}
}

func showError(msg string) {
	t, _ := windows.UTF16PtrFromString(appTitle)
	m, _ := windows.UTF16PtrFromString(msg)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}

func webviewDataDir() string {
	return filepath.Join(store.Dir(), "webview")
}

// relaunchElevated starts a new elevated copy of the app via UAC.
func relaunchElevated(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(append([]string{"--elevated"}, args...), " "))
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, dir, windows.SW_SHOWNORMAL)
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValue)
	return err == nil
}

func setAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue(runValue, `"`+exe+`" --minimized`)
}
