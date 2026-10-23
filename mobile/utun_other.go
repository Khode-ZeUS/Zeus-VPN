//go:build !ios

package zeuscore

// findTunFD: on Android the descriptor comes from VpnService.Builder.
func findTunFD() int { return -1 }
