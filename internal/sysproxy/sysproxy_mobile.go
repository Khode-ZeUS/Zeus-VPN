//go:build android || ios

// Package sysproxy is a no-op on Android and iOS: the mobile apps always run
// in Full VPN mode, where the operating system routes traffic to the tunnel.
package sysproxy

import "errors"

func Describe() string { return "none" }

func Enable(addr string, bypassIran, bypassLAN bool) error {
	return errors.New("the system proxy mode is not available on this platform")
}

func Restore() error { return nil }
