//go:build !windows && !(darwin && !ios)

package store

// keychainKey: Linux and the mobile apps keep the key in a private file of
// the data directory, which only this user (or app sandbox) can read.
func keychainKey() []byte { return nil }
