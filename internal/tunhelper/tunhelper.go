//go:build (darwin && !ios) || (linux && !android)

// Package tunhelper runs Full VPN mode on macOS and Linux without running the
// whole app as root. The app starts a copy of itself with administrator
// rights (`--tun-helper`); that helper creates the virtual adapter, points
// the system DNS at it, and sends every connection to the app's local
// SOCKS5/HTTP proxy over loopback. A loopback control connection ties the
// helper's life to the app: when it closes, the helper restores DNS and exits.
package tunhelper

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"zeusvpn/internal/vpn"
)

// Flag is the command-line switch that starts the helper.
const Flag = "--tun-helper"

type Config struct {
	// ProxyAddr is the app's local HTTP + SOCKS5 proxy.
	ProxyAddr string
	BypassLAN bool
	// OnExit is called if the helper stops without being asked to.
	OnExit func()
}

// Helper is the app's handle on a running helper process.
type Helper struct {
	conn     net.Conn
	proc     *os.Process // nil when the helper was detached (macOS)
	done     chan struct{}
	once     sync.Once
	stopping atomic.Bool
}

// Launch starts the helper with administrator rights (the OS asks for the
// password) and waits until the adapter is up.
func Launch(cfg Config, logf func(string)) (*Helper, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	defer ln.Close()
	tok := make([]byte, 16)
	rand.Read(tok)
	token := hex.EncodeToString(tok)
	args := []string{Flag, "--ctrl", ln.Addr().String(), "--token", token, "--proxy", cfg.ProxyAddr, "--parent", strconv.Itoa(os.Getpid())}
	if cfg.BypassLAN {
		args = append(args, "--bypass-lan")
	}
	logf("info starting the Full VPN helper (asks for the administrator password)")
	proc, exited, err := runElevated(exe, args)
	if err != nil {
		return nil, err
	}

	type accepted struct {
		c   net.Conn
		err error
	}
	acc := make(chan accepted, 1)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				acc <- accepted{nil, err}
				return
			}
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			line, err := bufio.NewReader(c).ReadString('\n')
			if err == nil && subtle.ConstantTimeCompare([]byte(strings.TrimSpace(line)), []byte(token)) == 1 {
				c.SetReadDeadline(time.Time{})
				acc <- accepted{c, nil}
				return
			}
			c.Close()
		}
	}()
	var conn net.Conn
	select {
	case a := <-acc:
		if a.err != nil {
			return nil, a.err
		}
		conn = a.c
	case err := <-exited:
		if err == nil {
			err = errors.New("administrator permission was not granted")
		}
		return nil, err
	case <-time.After(3 * time.Minute):
		if proc != nil {
			proc.Kill()
		}
		return nil, errors.New("timed out waiting for administrator permission")
	}

	h := &Helper{conn: conn, proc: proc, done: make(chan struct{})}
	ready := make(chan error, 1)
	go func() {
		defer close(h.done)
		sc := bufio.NewScanner(conn)
		readySent := false
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "ready":
				readySent = true
				ready <- nil
			case strings.HasPrefix(line, "error "):
				if !readySent {
					readySent = true
					ready <- errors.New(strings.TrimPrefix(line, "error "))
				} else {
					logf(line)
				}
			case strings.HasPrefix(line, "log "):
				logf(strings.TrimPrefix(line, "log "))
			}
		}
		if !readySent {
			ready <- errors.New("the Full VPN helper exited unexpectedly")
		} else if !h.stopping.Load() && cfg.OnExit != nil {
			cfg.OnExit()
		}
	}()
	select {
	case err := <-ready:
		if err != nil {
			h.Stop()
			return nil, fmt.Errorf("could not start Full VPN: %w", err)
		}
	case <-time.After(30 * time.Second):
		h.Stop()
		return nil, errors.New("the Full VPN helper did not start in time")
	}
	return h, nil
}

// Stop tells the helper to remove the adapter and waits for it to finish.
func (h *Helper) Stop() {
	h.once.Do(func() {
		h.stopping.Store(true)
		h.conn.SetWriteDeadline(time.Now().Add(time.Second))
		io.WriteString(h.conn, "stop\n")
		select {
		case <-h.done:
		case <-time.After(5 * time.Second):
		}
		h.conn.Close()
	})
}

// ---------- helper process ----------

// Run is the helper's main function; it returns the process exit code.
func Run(args []string) int {
	fs := flag.NewFlagSet("tun-helper", flag.ContinueOnError)
	ctrl := fs.String("ctrl", "", "")
	token := fs.String("token", "", "")
	proxy := fs.String("proxy", "", "")
	parent := fs.Int("parent", 0, "")
	bypassLAN := fs.Bool("bypass-lan", false, "")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	// Only loopback addresses: the helper runs as root.
	for _, a := range []string{*ctrl, *proxy} {
		ap, err := netip.ParseAddrPort(a)
		if err != nil || !ap.Addr().IsLoopback() {
			fmt.Fprintln(os.Stderr, "invalid address", a)
			return 2
		}
	}
	// Undo a DNS change left by a helper that crashed.
	restoreDNS()

	conn, err := net.DialTimeout("tcp", *ctrl, 5*time.Second)
	if err != nil {
		return 1
	}
	defer conn.Close()
	var wmu sync.Mutex
	send := func(s string) {
		wmu.Lock()
		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		io.WriteString(conn, strings.ReplaceAll(s, "\n", " ")+"\n")
		wmu.Unlock()
	}
	send(*token)

	mon, err := vpn.NewMonitor()
	if err != nil {
		send("error " + err.Error())
		return 1
	}
	defer mon.Close()
	v, err := vpn.Start(mon, vpn.Options{
		Dial:       socksDialer(*proxy),
		Forward:    httpForwarder(*proxy),
		ExcludeLAN: *bypassLAN,
		Log:        func(s string) { send("log " + s) },
	})
	if err != nil {
		send("error " + err.Error())
		return 1
	}
	defer v.Close()
	if err := setDNS(v.Name(), vpn.DNSServer.String()); err != nil {
		send("log warning could not point the system DNS at the VPN: " + err.Error())
	}
	defer restoreDNS()
	send("log info Full VPN adapter " + v.Name() + " is up")
	send("ready")

	stop := make(chan struct{})
	var stopOnce sync.Once
	quit := func() { stopOnce.Do(func() { close(stop) }) }
	go func() {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			if sc.Text() == "stop" {
				break
			}
		}
		quit()
	}()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	go func() { <-sig; quit() }()
	go func() {
		// The control connection normally closes when the app exits; this
		// also covers an app that hangs.
		for {
			time.Sleep(2 * time.Second)
			if *parent > 0 && syscall.Kill(*parent, 0) == syscall.ESRCH {
				quit()
				return
			}
		}
	}()
	<-stop
	return 0
}

// socksDialer connects through the app's SOCKS5 proxy, by host name when
// known so the VPN server resolves it.
func socksDialer(proxy string) func(ctx context.Context, target string) (net.Conn, error) {
	return func(ctx context.Context, target string) (net.Conn, error) {
		host, portStr, err := net.SplitHostPort(target)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, err
		}
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", proxy)
		if err != nil {
			return nil, err
		}
		if dl, ok := ctx.Deadline(); ok {
			c.SetDeadline(dl)
		}
		req := []byte{5, 1, 0, 5, 1, 0}
		if ip, err := netip.ParseAddr(host); err == nil && ip.Unmap().Is4() {
			a := ip.Unmap().As4()
			req = append(append(req, 1), a[:]...)
		} else if err == nil {
			a := ip.As16()
			req = append(append(req, 4), a[:]...)
		} else {
			if len(host) > 255 {
				c.Close()
				return nil, errors.New("host name too long")
			}
			req = append(append(req, 3, byte(len(host))), host...)
		}
		req = binary.BigEndian.AppendUint16(req, uint16(port))
		if _, err := c.Write(req); err != nil {
			c.Close()
			return nil, err
		}
		// Method selection (2 bytes) + reply header (4 bytes).
		resp := make([]byte, 6)
		if _, err := io.ReadFull(c, resp); err != nil {
			c.Close()
			return nil, err
		}
		if resp[0] != 5 || resp[1] != 0 || resp[3] != 0 {
			c.Close()
			return nil, fmt.Errorf("proxy refused %s (code %d)", target, resp[3])
		}
		var skip int
		switch resp[5] {
		case 1:
			skip = 4
		case 4:
			skip = 16
		case 3:
			l := make([]byte, 1)
			if _, err := io.ReadFull(c, l); err != nil {
				c.Close()
				return nil, err
			}
			skip = int(l[0])
		}
		if _, err := io.ReadFull(c, make([]byte, skip+2)); err != nil {
			c.Close()
			return nil, err
		}
		c.SetDeadline(time.Time{})
		return c, nil
	}
}

// httpForwarder sends plain HTTP requests through the app's HTTP proxy.
func httpForwarder(proxy string) func(*http.Request) (*http.Response, error) {
	u := &url.URL{Scheme: "http", Host: proxy}
	tr := &http.Transport{Proxy: http.ProxyURL(u), DisableCompression: true, IdleConnTimeout: 30 * time.Second}
	return tr.RoundTrip
}
