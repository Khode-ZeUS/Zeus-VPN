package store

import (
	"sync/atomic"
)

// The device vault key: the master key the mobile hosts unwrap from the
// device's secure store (Android Keystore / iOS Keychain hand the app a
// random 32-byte key at startup; the raw bytes live only in this process).
// It wins over every other key source and must be installed before the
// first account load. Desktops never set it and keep DPAPI / login keychain.

var vault atomic.Value // []byte when installed

// SetVaultKey installs the device key. Accepts exactly 32 bytes; anything
// else is ignored.
func SetVaultKey(k []byte) {
	if len(k) == 32 {
		vault.Store(append([]byte(nil), k...))
	}
}

// vaultKey returns the installed device key, or nil.
func vaultKey() []byte {
	if v := vault.Load(); v != nil {
		return v.([]byte)
	}
	return nil
}
