//go:build !windows

// Package killswitch is only implemented on Windows. On the other platforms
// Full VPN mode keeps the virtual adapter up while reconnecting, so traffic
// cannot leave outside the tunnel anyway.
package killswitch

import "errors"

// Supported reports whether this platform has a kill switch.
const Supported = false

func Active() bool { return false }

func Enable(tunLUID uint64, allowLAN bool) error {
	return errors.New("the kill switch is not available on this platform")
}

func Disable() {}
