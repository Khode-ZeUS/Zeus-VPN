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

// masterKey returns the 256-bit key that encrypts the account file. It is
// kept in the platform keychain when there is one (see keychainKey), and in
// a private file of the data directory otherwise.
func masterKey() ([]byte, error) {
	keyOnce.Do(func() {
		if k := keychainKey(); len(k) == 32 {
			key = k
			return
		}
		key, keyErr = fileKey()
	})
	return key, keyErr
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
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
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
