// Package vpn implements full-system VPN mode: a WinTun adapter with a
// userspace TCP/IP stack, auto-routing, and DNS hijacking.
package vpn

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"zeusvpn/internal/netx"
	"zeusvpn/internal/tunnel"

	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const (
	InterfaceName = "ZeusVPN"
	mtu           = 9000
)

// Binding tracks the physical default interface and binds our own sockets to
// it, so they bypass the VPN routes.
type Binding struct {
	finder *control.DefaultInterfaceFinder
	netMon tun.NetworkUpdateMonitor
	ifMon  tun.DefaultInterfaceMonitor
}

func NewBinding() (*Binding, error) {
	finder := control.NewDefaultInterfaceFinder()
	if err := finder.Update(); err != nil {
		return nil, err
	}
	netMon, err := tun.NewNetworkUpdateMonitor(logger.NOP())
	if err != nil {
		return nil, err
	}
	netMon.RegisterCallback(func() { finder.Update() })
	if err := netMon.Start(); err != nil {
		return nil, fmt.Errorf("network monitor: %w", err)
	}
	ifMon, err := tun.NewDefaultInterfaceMonitor(netMon, logger.NOP(), tun.DefaultInterfaceMonitorOptions{InterfaceFinder: finder})
	if err != nil {
		netMon.Close()
		return nil, err
	}
	if err := ifMon.Start(); err != nil {
		netMon.Close()
		return nil, fmt.Errorf("no internet connection detected: %w", err)
	}
	b := &Binding{finder: finder, netMon: netMon, ifMon: ifMon}
	netx.SetControl(control.BindToInterfaceFunc(finder, func(network, address string) (string, int, error) {
		iface := ifMon.DefaultInterface()
		if iface == nil {
			return "", -1, tun.ErrNoRoute
		}
		return iface.Name, iface.Index, nil
	}))
	return b, nil
}

func (b *Binding) Close() {
	netx.SetControl(nil)
	b.ifMon.Close()
	b.netMon.Close()
}

type Options struct {
	Upstream   *tunnel.Upstream
	Forward    tunnel.RoundTripFunc
	BypassIran bool
	Log        func(string)
}

type VPN struct {
	tunIf  tun.Tun
	stack  tun.Stack
	dns    *DoH
	cancel context.CancelFunc
}

var privateRanges = []netip.Prefix{
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
}

func Start(b *Binding, opts Options) (*VPN, error) {
	ctx, cancel := context.WithCancel(context.Background())
	v := &VPN{cancel: cancel, dns: NewDoH(opts.Upstream.Dial)}
	h := &handler{ctx: ctx, opts: opts, dns: v.dns}

	tunOpts := tun.Options{
		Name:                     InterfaceName,
		Inet4Address:             []netip.Prefix{netip.MustParsePrefix("172.19.0.1/30")},
		Inet6Address:             []netip.Prefix{netip.MustParsePrefix("fdfe:dcba:9876::1/126")},
		MTU:                      mtu,
		AutoRoute:                true,
		StrictRoute:              true,
		Inet4RouteExcludeAddress: privateRanges,
		InterfaceFinder:          b.finder,
		InterfaceMonitor:         b.ifMon,
		Logger:                   logger.NOP(),
		EXP_MultiPendingPackets:  true,
	}
	tunIf, err := tun.New(tunOpts)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("could not create the virtual network adapter: %w", err)
	}
	stack, err := tun.NewStack("gvisor", tun.StackOptions{
		Context:         ctx,
		Tun:             tunIf,
		TunOptions:      tunOpts,
		UDPTimeout:      5 * time.Minute,
		Handler:         h,
		Logger:          logger.NOP(),
		InterfaceFinder: b.finder,
	})
	if err != nil {
		tunIf.Close()
		cancel()
		return nil, err
	}
	if err := stack.Start(); err != nil {
		tunIf.Close()
		cancel()
		return nil, fmt.Errorf("network stack: %w", err)
	}
	if err := tunIf.Start(); err != nil {
		stack.Close()
		tunIf.Close()
		cancel()
		return nil, fmt.Errorf("could not set VPN routes: %w", err)
	}
	v.tunIf, v.stack = tunIf, stack
	return v, nil
}

func (v *VPN) Close() {
	v.cancel()
	v.stack.Close()
	v.tunIf.Close()
	v.dns.Close()
}

type handler struct {
	ctx  context.Context
	opts Options
	dns  *DoH
}

func (h *handler) JudgeFlow(network uint8, source, destination netip.AddrPort, firstPacket []byte) tun.FlowVerdict {
	switch network {
	case 6: // TCP
		return tun.FlowVerdict{Action: tun.ActionAccept}
	case 17: // UDP
		if destination.Port() == 53 {
			return tun.FlowVerdict{Action: tun.ActionHijackDNS}
		}
		// The proxy only carries TCP; rejecting UDP fast makes QUIC fall back to TCP.
		return tun.FlowVerdict{Action: tun.ActionReject}
	}
	return tun.FlowVerdict{Action: tun.ActionDrop}
}

func (h *handler) NewDNSPacket(payload []byte, source, destination M.Socksaddr, writer N.PacketWriter) {
	query := append([]byte(nil), payload...)
	go func() {
		ctx, cancel := context.WithTimeout(h.ctx, 10*time.Second)
		defer cancel()
		resp, err := h.dns.Exchange(ctx, query)
		if err != nil {
			if h.ctx.Err() == nil {
				h.opts.Log("warning DNS lookup failed: " + err.Error())
			}
			return
		}
		b := buf.NewSize(len(resp))
		b.Write(resp)
		writer.WritePacket(b, destination)
	}()
}

func (h *handler) NewConnectionEx(ctx context.Context, conn net.Conn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	err := h.handleTCP(conn, destination)
	conn.Close()
	if onClose != nil {
		onClose(err)
	}
}

func (h *handler) handleTCP(conn net.Conn, dst M.Socksaddr) error {
	port := dst.Port
	domain, early := sniff(conn, port)
	host := dst.Addr.Unmap().String()
	if domain != "" {
		host = domain
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(port)))

	// Plain HTTP can't be CONNECTed (the proxy refuses port 80), so forward
	// it request by request instead.
	if isHTTPMethodPrefix(early) {
		br := bufio.NewReader(io.MultiReader(bytes.NewReader(early), conn))
		tunnel.ServeHTTPForward(conn, br, nil, target, h.opts.Forward)
		return nil
	}

	ctx, cancel := context.WithTimeout(h.ctx, 20*time.Second)
	var up net.Conn
	var err error
	if h.opts.BypassIran && (strings.HasSuffix(domain, ".ir") || domain == "ir") {
		up, err = netx.DialContext(ctx, "tcp", net.JoinHostPort(dst.Addr.Unmap().String(), strconv.Itoa(int(port))))
	} else {
		up, err = h.opts.Upstream.Dial(ctx, target)
	}
	cancel()
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			h.opts.Log(fmt.Sprintf("warning failed to open tunnel to %s: %v", target, err))
		}
		return err
	}
	if len(early) > 0 {
		if _, err := up.Write(early); err != nil {
			up.Close()
			return err
		}
	}
	tunnel.Relay(conn, up)
	return nil
}

func (h *handler) NewPacketConnectionEx(ctx context.Context, conn N.PacketConn, source, destination M.Socksaddr, onClose N.CloseHandlerFunc) {
	conn.Close()
	if onClose != nil {
		onClose(errors.New("udp not supported"))
	}
}
