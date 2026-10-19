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
	AutoSwitch  bool   `json:"auto_switch"`
	TLSSplit    string `json:"tls_split"`
	KillSwitch  bool   `json:"kill_switch"`
	// Email is only read to migrate single-account installs.
	Email string `json:"email,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{Mode: ModeProxy, ProxyPort: 10820, BypassIran: true, BypassLAN: true, AutoSwitch: true, TLSSplit: "auto"}
}

// Account is one Mozilla account with its sign-in and last known data quota.
type Account struct {
	Email      string    `json:"email"`
	Tokens     Tokens    `json:"tokens"`
	QuotaLimit int64     `json:"quota_limit"`
	QuotaLeft  int64     `json:"quota_left"`
	QuotaReset time.Time `json:"quota_reset"`
	QuotaAt    time.Time `json:"quota_at"`
	// Exhausted is set when Mozilla refused service for lack of quota; it
	// clears once QuotaReset has passed.
	Exhausted  bool `json:"exhausted"`
	NeedsLogin bool `json:"needs_login"`
}

// Usable reports whether the account can currently be used for the VPN.
func (a *Account) Usable() bool {
	if a.NeedsLogin {
		return false
	}
	if a.Exhausted && (a.QuotaReset.IsZero() || time.Now().Before(a.QuotaReset)) {
		return false
	}
	return true
}

type Accounts struct {
	Active string    `json:"active"`
	List   []Account `json:"list"`
}

func (a *Accounts) Find(email string) *Account {
	for i := range a.List {
		if a.List[i].Email == email {
			return &a.List[i]
		}
	}
	return nil
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

// SaveJSON and LoadJSON keep non-secret caches (e.g. the server list).
func SaveJSON(name string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeAtomic(name, b)
}

func LoadJSON(name string, v any) bool {
	b, err := os.ReadFile(path(name))
	return err == nil && json.Unmarshal(b, v) == nil
}

func readProtected(name string, v any) bool {
	b, err := os.ReadFile(path(name))
	if err != nil {
		return false
	}
	plain, err := unprotect(b)
	if err != nil {
		return false
	}
	return json.Unmarshal(plain, v) == nil
}

func LoadAccounts() *Accounts {
	var a Accounts
	if readProtected("accounts.bin", &a) {
		return &a
	}
	// Migrate a single-account install (auth.bin) or a Python CLI session.
	var t *Tokens
	var single Tokens
	if readProtected("auth.bin", &single) && (single.AccessToken != "" || single.RefreshToken != "") {
		t = &single
	} else {
		t = importLegacyTokens()
	}
	if t != nil {
		email := LoadSettings().Email
		a = Accounts{Active: email, List: []Account{{Email: email, Tokens: *t}}}
		if SaveAccounts(&a) == nil {
			os.Remove(path("auth.bin"))
		}
	}
	return &a
}

func SaveAccounts(a *Accounts) error {
	plain, _ := json.Marshal(a)
	enc, err := protect(plain)
	if err != nil {
		return err
	}
	return writeAtomic("accounts.bin", enc)
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
	return &Tokens{
		AccessToken:  legacy.AccessToken,
		RefreshToken: legacy.RefreshToken,
		Scope:        legacy.Scope,
		ObtainedAt:   time.Unix(int64(legacy.ObtainedAt), 0),
		ExpiresIn:    legacy.ExpiresIn,
	}
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
