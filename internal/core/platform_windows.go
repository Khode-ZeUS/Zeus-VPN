package core

import "golang.org/x/sys/windows"

// TunNeedsElevation: Full VPN mode runs inside the app, so the whole app
// must run as administrator.
const TunNeedsElevation = true

func IsElevated() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

func defaultTunDriver() TunDriver { return &localTun{} }
