//go:build !windows

package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"sync"
)

var (
	keyOnce sync.Once
	key     []byte
	keyErr  error
)

// masterKey returns the 256-bit key that encrypts the account file: the
// device-protected key when the host installed one, else the platform
// keychain (see keychainKey), else a private file of the data directory.
func masterKey() ([]byte, error) {
	if k := vaultKey(); len(k) == 32 {
		return k, nil
	}
	keyOnce.Do(func() {
		if k := keychainKey(); len(k) == 32 {
			key = k
			return
		}
		key, keyErr = fileKey()
	})
	return key, keyErr
}

// aeadWith builds the account-file AEAD for one specific key (used by the
// key migration, which decrypts with the old key and re-protects with the
// new one).
func aeadWith(k []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func fileKey() ([]byte, error) {
	p := path("key")
	if b, err := os.ReadFile(p); err == nil {
		if k, err := hex.DecodeString(strings.TrimSpace(string(b))); err == nil && len(k) == 32 {
			return k, nil
		}
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := writeAtomic("key", []byte(hex.EncodeToString(k))); err != nil {
		return nil, err
	}
	return k, nil
}

// migrateKeyIfNeeded re-wraps the account file under the device key:
// installs that kept the master key next to the data (Android, iOS, Linux)
// move it into Android Keystore / iOS Keychain, and the old key file is
// removed. Runs once, only while a device key is installed.
func migrateKeyIfNeeded() {
	if vaultKey() == nil {
		return
	}
	oldKey, err := fileKey()
	if err != nil {
		return
	}
	b, err := os.ReadFile(path("accounts.bin"))
	if err != nil {
		os.Remove(path("key"))
		return
	}
	old, err := aeadWith(oldKey)
	if err != nil {
		return
	}
	if len(b) < old.NonceSize() {
		return
	}
	plain, err := old.Open(nil, b[:old.NonceSize()], b[old.NonceSize():], nil)
	if err != nil {
		return // not wrapped with the file key; leave it alone
	}
	neu, err := aead()
	if err != nil {
		return
	}
	nonce := make([]byte, neu.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return
	}
	enc := neu.Seal(nonce, nonce, plain, nil)
	if writeAtomic("accounts.bin", enc) == nil {
		os.Remove(path("key"))
	}
}

func newKey() []byte {
	k := make([]byte, 32)
	rand.Read(k)
	return k
}

func aead() (cipher.AEAD, error) {
	k, err := masterKey()
	if err != nil {
		return nil, err
	}
	return aeadWith(k)
}

func protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("nothing to protect")
	}
	g, err := aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return g.Seal(nonce, nonce, plain, nil), nil
}

func unprotect(enc []byte) ([]byte, error) {
	g, err := aead()
	if err != nil {
		return nil, err
	}
	if len(enc) < g.NonceSize() {
		return nil, errors.New("short blob")
	}
	return g.Open(nil, enc[:g.NonceSize()], enc[g.NonceSize():], nil)
}
