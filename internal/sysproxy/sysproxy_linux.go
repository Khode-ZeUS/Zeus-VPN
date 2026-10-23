//go:build linux && !android

// Package sysproxy switches the desktop proxy settings on Linux (GNOME and
// other GSettings desktops, and KDE Plasma) and restores whatever was
// configured before, including after a crash.
package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"zeusvpn/internal/store"
)

const backupFile = "sysproxy-backup.json"

type backup struct {
	// GNOME maps "schema key" to the GVariant text gsettings printed.
	GNOME map[string]string `json:"gnome,omitempty"`
	// KDE maps kioslaverc "Proxy Settings" keys to their values.
	KDE       map[string]string `json:"kde,omitempty"`
	KDEUsed   bool              `json:"kde_used"`
	OurServer string            `json:"our_server"`
}

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

// ---------- GNOME (GSettings) ----------

var gnomeKeys = [][2]string{
	{"org.gnome.system.proxy", "mode"},
	{"org.gnome.system.proxy", "ignore-hosts"},
	{"org.gnome.system.proxy.http", "host"},
	{"org.gnome.system.proxy.http", "port"},
	{"org.gnome.system.proxy.https", "host"},
	{"org.gnome.system.proxy.https", "port"},
	{"org.gnome.system.proxy.socks", "host"},
	{"org.gnome.system.proxy.socks", "port"},
}

func hasGNOME() bool {
	if _, err := exec.LookPath("gsettings"); err != nil {
		return false
	}
	_, err := run("gsettings", "get", "org.gnome.system.proxy", "mode")
	return err == nil
}

func gnomeRead() map[string]string {
	m := map[string]string{}
	for _, k := range gnomeKeys {
		if v, err := run("gsettings", "get", k[0], k[1]); err == nil {
			m[k[0]+" "+k[1]] = v
		}
	}
	return m
}

func gnomeWrite(m map[string]string) error {
	var errs []error
	// Write the mode last so apps never see "manual" with a stale host.
	for _, k := range append(gnomeKeys[1:], gnomeKeys[0]) {
		v, ok := m[k[0]+" "+k[1]]
		if !ok {
			continue
		}
		if _, err := run("gsettings", "set", k[0], k[1], v); err != nil {
			errs = append(errs, fmt.Errorf("gsettings set %s %s: %w", k[0], k[1], err))
		}
	}
	return errors.Join(errs...)
}

func gvariantList(items []string) string {
	q := make([]string, len(items))
	for i, s := range items {
		q[i] = "'" + s + "'"
	}
	return "[" + strings.Join(q, ", ") + "]"
}

// ---------- KDE Plasma (kioslaverc) ----------

var kdeKeys = []string{"ProxyType", "httpProxy", "httpsProxy", "socksProxy", "NoProxyFor"}

func kdeTool(prefix string) string {
	for _, v := range []string{"6", "5", ""} {
		if p, err := exec.LookPath(prefix + v); err == nil {
			return p
		}
	}
	return ""
}

func hasKDE() bool {
	desktop := strings.ToUpper(os.Getenv("XDG_CURRENT_DESKTOP") + ":" + os.Getenv("DESKTOP_SESSION"))
	return strings.Contains(desktop, "KDE") && kdeTool("kwriteconfig") != ""
}

func kdeRead() map[string]string {
	tool := kdeTool("kreadconfig")
	m := map[string]string{}
	if tool == "" {
		return m
	}
	for _, k := range kdeKeys {
		v, _ := run(tool, "--file", "kioslaverc", "--group", "Proxy Settings", "--key", k)
		m[k] = v
	}
	return m
}

func kdeWrite(m map[string]string) error {
	tool := kdeTool("kwriteconfig")
	var errs []error
	for _, k := range kdeKeys {
		v, ok := m[k]
		if !ok {
			continue
		}
		args := []string{"--file", "kioslaverc", "--group", "Proxy Settings", "--key", k}
		if v == "" {
			args = append(args, "--delete")
		} else {
			args = append(args, v)
		}
		if _, err := run(tool, args...); err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", tool, k, err))
		}
	}
	// Tell running KDE apps to re-read the proxy settings.
	run("dbus-send", "--type=signal", "/KIO/Scheduler", "org.kde.KIO.Scheduler.reparseSlaveConfiguration", "string:")
	return errors.Join(errs...)
}

// Describe summarizes the current system proxy for logs.
func Describe() string {
	if hasGNOME() {
		mode, _ := run("gsettings", "get", "org.gnome.system.proxy", "mode")
		return "gnome mode " + mode
	}
	if hasKDE() {
		return "kde proxy type " + kdeRead()["ProxyType"]
	}
	return "unknown"
}

// Enable points the desktop proxy at our local HTTP + SOCKS5 proxy,
// remembering the previous configuration first.
func Enable(addr string, bypassIran, bypassLAN bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	gnome, kde := hasGNOME(), hasKDE()
	if !gnome && !kde {
		return fmt.Errorf("this desktop has no proxy settings Zeus VPN can change (GNOME or KDE). Use Full VPN mode, or set the proxy %s (HTTP or SOCKS5) in your apps", addr)
	}
	var prev backup
	// Don't overwrite a backup of our own settings (e.g. reconnects).
	if !store.LoadJSON(backupFile, &prev) || prev.OurServer != addr {
		prev = backup{OurServer: addr}
		if gnome {
			prev.GNOME = gnomeRead()
		}
		if kde {
			prev.KDE, prev.KDEUsed = kdeRead(), true
		}
		if err := store.SaveJSON(backupFile, prev); err != nil {
			return err
		}
	}
	bypass := []string{"localhost", "127.0.0.0/8", "::1", "169.254.0.0/16"}
	if bypassLAN {
		bypass = append(bypass, "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "*.local")
	}
	if bypassIran {
		bypass = append(bypass, "*.ir")
	}
	var errs []error
	if gnome {
		errs = append(errs, gnomeWrite(map[string]string{
			"org.gnome.system.proxy mode":         "'manual'",
			"org.gnome.system.proxy ignore-hosts": gvariantList(bypass),
			"org.gnome.system.proxy.http host":    "'" + host + "'",
			"org.gnome.system.proxy.http port":    port,
			"org.gnome.system.proxy.https host":   "'" + host + "'",
			"org.gnome.system.proxy.https port":   port,
			"org.gnome.system.proxy.socks host":   "'" + host + "'",
			"org.gnome.system.proxy.socks port":   port,
		}))
	}
	if kde {
		errs = append(errs, kdeWrite(map[string]string{
			"ProxyType":  "1",
			"httpProxy":  "http://" + host + " " + port,
			"httpsProxy": "http://" + host + " " + port,
			"socksProxy": "socks://" + host + " " + port,
			"NoProxyFor": strings.Join(bypass, ","),
		}))
	}
	return errors.Join(errs...)
}

// Restore puts back the configuration saved by Enable. It is a no-op if the
// user changed the proxy to something else in the meantime.
func Restore() error {
	var b backup
	if !store.LoadJSON(backupFile, &b) {
		return nil
	}
	_, ourPort, _ := net.SplitHostPort(b.OurServer)
	var errs []error
	if len(b.GNOME) > 0 && hasGNOME() {
		port, _ := run("gsettings", "get", "org.gnome.system.proxy.http", "port")
		mode, _ := run("gsettings", "get", "org.gnome.system.proxy", "mode")
		if mode != "'manual'" || port == ourPort {
			errs = append(errs, gnomeWrite(b.GNOME))
		}
	}
	if b.KDEUsed && kdeTool("kwriteconfig") != "" {
		if cur := kdeRead(); cur["ProxyType"] != "1" || strings.HasSuffix(cur["httpProxy"], " "+ourPort) {
			errs = append(errs, kdeWrite(b.KDE))
		}
	}
	store.ClearProxyBackup()
	return errors.Join(errs...)
}
