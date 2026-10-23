//go:build darwin && !ios

package store

import (
	"encoding/hex"
	"os"
	"os/exec"
	"strings"
)

const (
	keychainService = "Zeus VPN"
	keychainAccount = "account-key"
)

// keychainKey keeps the account key in the user's login keychain. It returns
// nil when the keychain is unavailable (e.g. when running as root), and the
// key file is used instead.
func keychainKey() []byte {
	if os.Geteuid() == 0 {
		return nil
	}
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", keychainService, "-a", keychainAccount, "-w").Output()
	if err == nil {
		if k, err := hex.DecodeString(strings.TrimSpace(string(out))); err == nil && len(k) == 32 {
			return k
		}
	}
	k := newKey()
	if exec.Command("/usr/bin/security", "add-generic-password", "-U", "-s", keychainService, "-a", keychainAccount, "-w", hex.EncodeToString(k)).Run() != nil {
		return nil
	}
	return k
}
