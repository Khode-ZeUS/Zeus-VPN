// Package store persists settings and credentials under %APPDATA%\ZeusVPN.
// OAuth tokens are encrypted with DPAPI so only the current Windows user can
// read them.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	ModeProxy = "proxy"
	ModeTUN   = "tun"
)

type Settings struct {
	Mode        string `json:"mode"`
	CountryCode string `json:"country_code"`
	CityCode    string `json:"city_code"`
	ProxyPort   int    `json:"proxy_port"`
	BypassIran  bool   `json:"bypass_iran"`
	BypassLAN   bool   `json:"bypass_lan"`
	AutoConnect bool   `json:"auto_connect"`
	Email       string `json:"email"`
}

func DefaultSettings() Settings {
	return Settings{Mode: ModeProxy, ProxyPort: 10820, BypassIran: true, BypassLAN: true}
}

type Tokens struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Scope        string    `json:"scope"`
	ObtainedAt   time.Time `json:"obtained_at"`
	ExpiresIn    int64     `json:"expires_in"`
}

// Valid reports whether the access token has at least two minutes left.
func (t *Tokens) Valid() bool {
	if t == nil || t.AccessToken == "" {
		return false
	}
	return time.Now().Before(t.ObtainedAt.Add(time.Duration(t.ExpiresIn)*time.Second - 2*time.Minute))
}

func Dir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	d := filepath.Join(base, "ZeusVPN")
	os.MkdirAll(d, 0o700)
	return d
}

func path(name string) string { return filepath.Join(Dir(), name) }

func writeAtomic(name string, data []byte) error {
	tmp := path(name + ".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path(name))
}

func LoadSettings() Settings {
	s := DefaultSettings()
	if b, err := os.ReadFile(path("settings.json")); err == nil {
		json.Unmarshal(b, &s)
	}
	if s.Mode != ModeTUN {
		s.Mode = ModeProxy
	}
	if s.ProxyPort <= 0 || s.ProxyPort > 65535 {
		s.ProxyPort = 10820
	}
	return s
}

func SaveSettings(s Settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return writeAtomic("settings.json", b)
}

func LoadTokens() *Tokens {
	if b, err := os.ReadFile(path("auth.bin")); err == nil {
		if plain, err := unprotect(b); err == nil {
			var t Tokens
			if json.Unmarshal(plain, &t) == nil && (t.AccessToken != "" || t.RefreshToken != "") {
				return &t
			}
		}
	}
	return importLegacyTokens()
}

func SaveTokens(t *Tokens) error {
	plain, _ := json.Marshal(t)
	enc, err := protect(plain)
	if err != nil {
		return err
	}
	return writeAtomic("auth.bin", enc)
}

func DeleteTokens() {
	os.Remove(path("auth.bin"))
}

// importLegacyTokens picks up a session from the Python FoxyVPN CLI
// (~/.foxyvpn-tokens.json) so existing users don't have to sign in again.
func importLegacyTokens() *Tokens {
	marker := path("legacy-import-done")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	os.WriteFile(marker, nil, 0o600)
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(home, ".foxyvpn-tokens.json"))
	if err != nil {
		return nil
	}
	var legacy struct {
		AccessToken  string  `json:"access_token"`
		RefreshToken string  `json:"refresh_token"`
		Scope        string  `json:"scope"`
		ObtainedAt   float64 `json:"obtained_at"`
		ExpiresIn    int64   `json:"expires_in"`
	}
	if json.Unmarshal(b, &legacy) != nil || (legacy.AccessToken == "" && legacy.RefreshToken == "") {
		return nil
	}
	t := &Tokens{
		AccessToken:  legacy.AccessToken,
		RefreshToken: legacy.RefreshToken,
		Scope:        legacy.Scope,
		ObtainedAt:   time.Unix(int64(legacy.ObtainedAt), 0),
		ExpiresIn:    legacy.ExpiresIn,
	}
	SaveTokens(t)
	return t
}

// ProxyBackup records the system proxy configuration we replaced, so it can
// be restored after a crash.
type ProxyBackup struct {
	Flags     uint32 `json:"flags"`
	Server    string `json:"server"`
	Bypass    string `json:"bypass"`
	AutoURL   string `json:"auto_url"`
	OurServer string `json:"our_server"`
}

func SaveProxyBackup(b *ProxyBackup) error {
	data, _ := json.Marshal(b)
	return writeAtomic("sysproxy-backup.json", data)
}

func LoadProxyBackup() (*ProxyBackup, error) {
	data, err := os.ReadFile(path("sysproxy-backup.json"))
	if err != nil {
		return nil, err
	}
	var b ProxyBackup
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func ClearProxyBackup() { os.Remove(path("sysproxy-backup.json")) }

func protect(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return nil, errors.New("nothing to protect")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}

func unprotect(enc []byte) ([]byte, error) {
	if len(enc) == 0 {
		return nil, errors.New("empty blob")
	}
	in := windows.DataBlob{Size: uint32(len(enc)), Data: &enc[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return append([]byte(nil), unsafe.Slice(out.Data, out.Size)...), nil
}
