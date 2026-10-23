//go:build darwin && !ios

package tunhelper

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
)

// dnsBackupFile is in a root-only directory, so it survives a crash for the
// next helper to restore.
const dnsBackupFile = "/var/run/zeusvpn-dns.json"

func networksetup(args ...string) (string, error) {
	out, err := exec.Command("/usr/sbin/networksetup", args...).CombinedOutput()
	if err != nil {
		return "", errors.New(strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func dnsServices() []string {
	out, err := networksetup("-listallnetworkservices")
	if err != nil {
		return nil
	}
	var list []string
	for i, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if i == 0 || line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		list = append(list, line)
	}
	return list
}

func flushDNS() {
	exec.Command("/usr/bin/dscacheutil", "-flushcache").Run()
	exec.Command("/usr/bin/killall", "-HUP", "mDNSResponder").Run()
}

// setDNS points every network service at the VPN's DNS address, which the
// adapter answers with DNS over HTTPS through the tunnel.
func setDNS(ifname, server string) error {
	backup := map[string][]string{}
	for _, svc := range dnsServices() {
		out, err := networksetup("-getdnsservers", svc)
		if err != nil {
			continue
		}
		var servers []string
		if !strings.HasPrefix(out, "There aren't any") {
			servers = strings.Fields(out)
		}
		backup[svc] = servers
	}
	if len(backup) == 0 {
		return errors.New("no network services found")
	}
	b, _ := json.Marshal(backup)
	if err := os.WriteFile(dnsBackupFile, b, 0o600); err != nil {
		return err
	}
	var errs []error
	for svc := range backup {
		if _, err := networksetup("-setdnsservers", svc, server); err != nil {
			errs = append(errs, err)
		}
	}
	flushDNS()
	return errors.Join(errs...)
}

func restoreDNS() {
	b, err := os.ReadFile(dnsBackupFile)
	if err != nil {
		return
	}
	var backup map[string][]string
	if json.Unmarshal(b, &backup) == nil {
		for svc, servers := range backup {
			if len(servers) == 0 {
				servers = []string{"Empty"}
			}
			networksetup(append([]string{"-setdnsservers", svc}, servers...)...)
		}
	}
	os.Remove(dnsBackupFile)
	flushDNS()
}
