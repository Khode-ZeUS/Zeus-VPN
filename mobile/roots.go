package zeuscore

import (
	_ "embed"
	"os"
	"path/filepath"
	"runtime"
)

// extra-roots.pem is Mozilla's CA bundle (curl.se/ca/cacert.pem). Go on
// Android only reads /system/etc/security/cacerts — the updated store of
// Android 10+ (/apex/com.android.conscrypt/cacerts) and newer roots that
// Fastly's chain uses (GlobalSign R46/E46) are missing there, so handshakes
// to Mozilla's endpoints fail with "certificate signed by unknown
// authority" on those devices. Writing the bundle out and pointing
// SSL_CERT_FILE / SSL_CERT_DIR at it (before the first TLS) fixes the trust
// without weakening verification.
//
//go:embed extra-roots.pem
var extraRoots []byte

// useAndroidRoots prepares the certificate roots on Android. Call it before
// the first TLS connection of the process.
func useAndroidRoots(dataDir string) {
	if runtime.GOOS != "android" {
		return
	}
	_ = os.MkdirAll(dataDir, 0o700)
	file := filepath.Join(dataDir, "extra-roots.pem")
	if err := os.WriteFile(file, extraRoots, 0o600); err != nil {
		return
	}
	store := "/system/etc/security/cacerts"
	if _, err := os.Stat("/apex/com.android.conscrypt/cacerts"); err == nil {
		store = "/apex/com.android.conscrypt/cacerts"
	}
	os.Setenv("SSL_CERT_DIR", store+":/data/misc/keychain/certs-added")
	os.Setenv("SSL_CERT_FILE", file)
}
