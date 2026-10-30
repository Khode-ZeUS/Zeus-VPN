//go:build darwin && !ios

// Package sysproxy switches the macOS system proxy of every enabled network
// service (with networksetup) and restores whatever was configured before,
// including after a crash.
package sysproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strings"
	"time"

	"zeusvpn/internal/store"
)

const backupFile = "sysproxy-backup.json"

type proxyConf struct {
	Enabled bool   `json:"enabled"`
	Server  string `json:"server"`
	Port    string `json:"port"`
}

type serviceConf struct {
	Web    proxyConf `json:"web"`
	Secure proxyConf `json:"secure"`
	Socks  proxyConf `json:"socks"`
	Bypass []string  `json:"bypass"`
}

type backup struct {
	Services  map[string]serviceConf `json:"services"`
	OurServer string                 `json:"our_server"`
}

func networksetup(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/sbin/networksetup", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("networksetup %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// services lists the enabled network services (Wi-Fi, Ethernet, ...).
func services() ([]string, error) {
	out, err := networksetup("-listallnetworkservices")
	if err != nil {
		return nil, err
	}
	var list []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		// The first line is a notice; disabled services start with '*'.
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		list = append(list, line)
	}
	if len(list) == 0 {
		return nil, errors.New("no network services found")
	}
	return list, nil
}

func readProxy(flag, svc string) proxyConf {
	out, err := networksetup(flag, svc)
	if err != nil {
		return proxyConf{}
	}
	var c proxyConf
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "Enabled":
			c.Enabled = v == "Yes"
		case "Server":
			c.Server = v
		case "Port":
			c.Port = v
		}
	}
	return c
}

func readService(svc string) serviceConf {
	c := serviceConf{
		Web:    readProxy("-getwebproxy", svc),
		Secure: readProxy("-getsecurewebproxy", svc),
		Socks:  readProxy("-getsocksfirewallproxy", svc),
	}
	if out, err := networksetup("-getproxybypassdomains", svc); err == nil && !strings.HasPrefix(out, "There aren't any") {
		for _, d := range strings.Fields(out) {
			c.Bypass = append(c.Bypass, d)
		}
	}
	return c
}

func writeProxy(setFlag, stateFlag, svc string, c proxyConf) error {
	if c.Server != "" && c.Port != "" && c.Port != "0" {
		if _, err := networksetup(setFlag, svc, c.Server, c.Port); err != nil {
			return err
		}
	}
	state := "off"
	if c.Enabled {
		state = "on"
	}
	_, err := networksetup(stateFlag, svc, state)
	return err
}

func writeService(svc string, c serviceConf) error {
	errs := []error{
		writeProxy("-setwebproxy", "-setwebproxystate", svc, c.Web),
		writeProxy("-setsecurewebproxy", "-setsecurewebproxystate", svc, c.Secure),
		writeProxy("-setsocksfirewallproxy", "-setsocksfirewallproxystate", svc, c.Socks),
	}
	bypass := c.Bypass
	if len(bypass) == 0 {
		bypass = []string{"Empty"}
	}
	_, err := networksetup(append([]string{"-setproxybypassdomains", svc}, bypass...)...)
	errs = append(errs, err)
	return errors.Join(errs...)
}

// Describe summarizes the current system proxy for logs.
func Describe() string {
	list, err := services()
	if err != nil {
		return "unknown"
	}
	var parts []string
	for _, svc := range list {
		if c := readProxy("-getwebproxy", svc); c.Enabled {
			parts = append(parts, svc+": proxy "+c.Server+":"+c.Port)
		}
	}
	if len(parts) == 0 {
		return "direct"
	}
	return strings.Join(parts, ", ")
}

// Enable points the proxy of every network service at our local HTTP +
// SOCKS5 proxy, remembering the previous configuration first.
func Enable(addr string, bypassIran, bypassLAN bool) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	list, err := services()
	if err != nil {
		return err
	}
	var prev backup
	// Don't overwrite a backup of our own settings (e.g. reconnects).
	if !store.LoadJSON(backupFile, &prev) || prev.OurServer != addr {
		prev = backup{Services: map[string]serviceConf{}, OurServer: addr}
		for _, svc := range list {
			prev.Services[svc] = readService(svc)
		}
		if err := store.SaveJSON(backupFile, prev); err != nil {
			return err
		}
	}
	bypass := []string{"localhost", "127.0.0.1", "::1", "*.local", "169.254/16"}
	if bypassLAN {
		bypass = append(bypass, "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16")
	}
	if bypassIran {
		bypass = append(bypass, "*.ir")
	}
	ours := proxyConf{Enabled: true, Server: host, Port: port}
	var errs []error
	for _, svc := range list {
		if err := writeService(svc, serviceConf{Web: ours, Secure: ours, Socks: ours, Bypass: bypass}); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) == len(list) {
		return errors.Join(errs...)
	}
	return nil
}

// Restore puts back the configuration saved by Enable. Services whose proxy
// the user changed to something else in the meantime are left alone.
func Restore() error {
	var b backup
	if !store.LoadJSON(backupFile, &b) {
		return nil
	}
	ourHost, ourPort, _ := net.SplitHostPort(b.OurServer)
	var errs []error
	for svc, prev := range b.Services {
		cur := readProxy("-getwebproxy", svc)
		if cur.Enabled && (cur.Server != ourHost || cur.Port != ourPort) {
			continue
		}
		if err := writeService(svc, prev); err != nil {
			errs = append(errs, err)
		}
	}
	store.ClearProxyBackup()
	return errors.Join(errs...)
}

// SystemProxy is not implemented on darwin: the control-plane fallback only
// applies on Windows for now.
func SystemProxy() (string, bool) { return "", false }
