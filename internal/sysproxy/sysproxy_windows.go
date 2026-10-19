// Package sysproxy switches the Windows (WinINet) system proxy and restores
// whatever was configured before, including after a crash.
package sysproxy

import (
	"fmt"
	"strings"
	"unsafe"

	"zeusvpn/internal/store"

	"golang.org/x/sys/windows"
)

const (
	optionPerConnectionOption  = 75
	optionProxySettingsChanged = 95
	optionRefresh              = 37

	perConnFlags         = 1
	perConnProxyServer   = 2
	perConnProxyBypass   = 3
	perConnAutoConfigURL = 4

	proxyTypeDirect  = 0x1
	proxyTypeProxy   = 0x2
	proxyTypeAutoURL = 0x4
)

var (
	wininet                 = windows.NewLazySystemDLL("wininet.dll")
	procInternetSetOption   = wininet.NewProc("InternetSetOptionW")
	procInternetQueryOption = wininet.NewProc("InternetQueryOptionW")
	procGlobalFree          = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")
)

// perConnOption mirrors INTERNET_PER_CONN_OPTIONW on 64-bit Windows.
type perConnOption struct {
	option uint32
	_      uint32
	value  uintptr
}

// perConnList mirrors INTERNET_PER_CONN_OPTION_LISTW.
type perConnList struct {
	size        uint32
	connection  *uint16
	optionCount uint32
	optionError uint32
	options     *perConnOption
}

type config struct {
	flags   uint32
	server  string
	bypass  string
	autoURL string
}

func setConfig(c config) error {
	opts := []perConnOption{
		{option: perConnFlags, value: uintptr(c.flags)},
		{option: perConnProxyServer, value: uintptr(unsafe.Pointer(utf16(c.server)))},
		{option: perConnProxyBypass, value: uintptr(unsafe.Pointer(utf16(c.bypass)))},
		{option: perConnAutoConfigURL, value: uintptr(unsafe.Pointer(utf16(c.autoURL)))},
	}
	list := perConnList{optionCount: uint32(len(opts)), options: &opts[0]}
	list.size = uint32(unsafe.Sizeof(list))
	r, _, err := procInternetSetOption.Call(0, optionPerConnectionOption, uintptr(unsafe.Pointer(&list)), uintptr(list.size))
	if r == 0 {
		return fmt.Errorf("InternetSetOption: %w", err)
	}
	procInternetSetOption.Call(0, optionProxySettingsChanged, 0, 0)
	procInternetSetOption.Call(0, optionRefresh, 0, 0)
	return nil
}

func getConfig() (config, error) {
	opts := []perConnOption{{option: perConnFlags}, {option: perConnProxyServer}, {option: perConnProxyBypass}, {option: perConnAutoConfigURL}}
	list := perConnList{optionCount: uint32(len(opts)), options: &opts[0]}
	list.size = uint32(unsafe.Sizeof(list))
	size := list.size
	r, _, err := procInternetQueryOption.Call(0, optionPerConnectionOption, uintptr(unsafe.Pointer(&list)), uintptr(unsafe.Pointer(&size)))
	if r == 0 {
		return config{}, fmt.Errorf("InternetQueryOption: %w", err)
	}
	str := func(o *perConnOption) string {
		p := *(**uint16)(unsafe.Pointer(&o.value))
		if p == nil {
			return ""
		}
		s := windows.UTF16PtrToString(p)
		procGlobalFree.Call(o.value)
		return s
	}
	return config{
		flags:   uint32(opts[0].value),
		server:  str(&opts[1]),
		bypass:  str(&opts[2]),
		autoURL: str(&opts[3]),
	}, nil
}

func utf16(s string) *uint16 {
	if s == "" {
		return nil
	}
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// Describe summarizes the current system proxy for logs.
func Describe() string {
	c, err := getConfig()
	if err != nil {
		return "unknown"
	}
	var parts []string
	if c.flags&proxyTypeProxy != 0 && c.server != "" {
		parts = append(parts, "proxy "+c.server)
	}
	if c.flags&proxyTypeAutoURL != 0 && c.autoURL != "" {
		parts = append(parts, "PAC "+c.autoURL)
	}
	if len(parts) == 0 {
		return "direct"
	}
	return strings.Join(parts, ", ")
}

// Enable points the system proxy at our local HTTP proxy, remembering the
// previous configuration first.
func Enable(addr string, bypassIran, bypassLAN bool) error {
	if prev, err := getConfig(); err == nil {
		// Don't overwrite a backup of our own settings (e.g. reconnects).
		if b, berr := store.LoadProxyBackup(); berr != nil || b.OurServer != prev.server {
			store.SaveProxyBackup(&store.ProxyBackup{Flags: prev.flags, Server: prev.server, Bypass: prev.bypass, AutoURL: prev.autoURL, OurServer: addr})
		}
	}
	bypass := []string{"localhost", "127.*", "<local>"}
	if bypassLAN {
		bypass = append(bypass, "10.*", "192.168.*", "169.254.*")
		for i := 16; i <= 31; i++ {
			bypass = append(bypass, fmt.Sprintf("172.%d.*", i))
		}
	}
	if bypassIran {
		bypass = append(bypass, "*.ir")
	}
	return setConfig(config{flags: proxyTypeDirect | proxyTypeProxy, server: addr, bypass: strings.Join(bypass, ";")})
}

// Restore puts back the configuration saved by Enable. It is a no-op if the
// user changed the proxy to something else in the meantime.
func Restore() error {
	b, err := store.LoadProxyBackup()
	if err != nil {
		return nil
	}
	cur, err := getConfig()
	if err == nil && cur.flags&proxyTypeProxy != 0 && cur.server != b.OurServer {
		store.ClearProxyBackup()
		return nil
	}
	if b.Flags == 0 {
		b.Flags = proxyTypeDirect
	}
	if err := setConfig(config{flags: b.Flags, server: b.Server, bypass: b.Bypass, autoURL: b.AutoURL}); err != nil {
		return err
	}
	store.ClearProxyBackup()
	return nil
}
