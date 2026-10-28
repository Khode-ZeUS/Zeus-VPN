//go:build !windows

package usage

// owner is unsupported here: connections stay uncounted until a per-app
// lookup exists for this platform.
func owner(local string) string { return "" }
