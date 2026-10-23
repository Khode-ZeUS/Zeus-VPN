// Package vpn implements full-system VPN mode: a virtual adapter (WinTun on
// Windows, utun on macOS, /dev/net/tun on Linux, or a descriptor handed over
// by the Android/iOS VPN APIs) with a userspace TCP/IP stack, auto-routing,
// and DNS hijacking.
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
	"runtime"
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

// Addresses of the virtual adapter. DNSServer is the address the host OS is
// told to use for DNS; every DNS query sent into the tunnel is answered.
var (
	Inet4Prefix = netip.MustParsePrefix("172.19.0.1/30")
	Inet6Prefix = netip.MustParsePrefix("fdfe:dcba:9876::1/126")
	DNSServer   = netip.MustParseAddr("172.19.0.2")
)

// Binding tracks the physical default interface and binds our own sockets to
// it, so they bypass the VPN routes.
type Binding struct {
	finder *control.DefaultInterfaceFinder
	netMon tun.NetworkUpdateMonitor
	ifMon  tun.DefaultInterfaceMonitor
	bound  bool
}

// NewBinding watches the default interface and binds this process's
// outbound sockets to it.
func NewBinding() (*Binding, error) {
	b, err := NewMonitor()
	if err != nil {
		return nil, err
	}
	b.bound = true
	netx.SetControl(control.BindToInterfaceFunc(b.finder, func(network, address string) (string, int, error) {
		iface := b.ifMon.DefaultInterface()
		if iface == nil {
			return "", -1, tun.ErrNoRoute
		}
		return iface.Name, iface.Index, nil
	}))
	return b, nil
}

// NewMonitor only watches the default interface, which auto-routing needs.
// The TUN helper process uses it: its own sockets go to the app over
// loopback and must not be bound to the physical interface.
func NewMonitor() (*Binding, error) {
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
	return &Binding{finder: finder, netMon: netMon, ifMon: ifMon}, nil
}

func (b *Binding) Close() {
	if b.bound {
		netx.SetControl(nil)
	}
	b.ifMon.Close()
	b.netMon.Close()
}

type Options struct {
	// Dial opens a TCP connection through the VPN; DNS over HTTPS uses it too.
	Dial    tunnel.DialFunc
	Forward tunnel.RoundTripFunc
	// BypassIran connects .ir sites directly (through netx).
	BypassIran bool
	// FD is a TUN descriptor opened by the Android or iOS VPN API. The host
	// app then configures addresses, routes and DNS itself.
	FD int
	// MTU overrides the default MTU (for FD).
	MTU int
	// ExcludeLAN keeps private networks off the auto-routes.
	ExcludeLAN bool
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

// linkLocalRanges stay off the VPN even when the LAN is routed through it.
var linkLocalRanges = []netip.Prefix{
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("255.255.255.255/32"),
}

// Start creates the adapter (or adopts opts.FD) and starts the stack. b is
// the interface monitor for auto-routing; it is unused (and may be nil) with
// opts.FD.
func Start(b *Binding, opts Options) (*VPN, error) {
	ctx, cancel := context.WithCancel(context.Background())
	v := &VPN{cancel: cancel, dns: NewDoH(opts.Dial)}
	h := &handler{ctx: ctx, opts: opts, dns: v.dns}

	tunOpts := tun.Options{
		Name:                    InterfaceName,
		Inet4Address:            []netip.Prefix{Inet4Prefix},
		Inet6Address:            []netip.Prefix{Inet6Prefix},
		MTU:                     mtu,
		Logger:                  logger.NOP(),
		EXP_MultiPendingPackets: true,
	}
	var finder control.InterfaceFinder
	if opts.FD > 0 {
		tunOpts.FileDescriptor = opts.FD
		tunOpts.EXP_ExternalConfiguration = true
		if opts.MTU > 0 {
			tunOpts.MTU = uint32(opts.MTU)
		}
		// Never updated: listing interfaces is not allowed on Android.
		finder = control.NewDefaultInterfaceFinder()
		tunOpts.InterfaceFinder = finder
		if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
			tunOpts.Name = "utun"
		}
	} else {
		if runtime.GOOS == "darwin" {
			tunOpts.Name = tun.CalculateInterfaceName("")
		}
		tunOpts.AutoRoute = true
		// Linux policy routing: the table and rule priorities sing-box uses.
		tunOpts.IPRoute2TableIndex = tun.DefaultIPRoute2TableIndex
		tunOpts.IPRoute2RuleIndex = tun.DefaultIPRoute2RuleIndex
		// On Windows strict routing also stops DNS queries from leaking
		// through the physical adapter.
		tunOpts.StrictRoute = runtime.GOOS == "windows"
		if opts.ExcludeLAN || runtime.GOOS == "windows" {
			tunOpts.Inet4RouteExcludeAddress = privateRanges
		} else {
			tunOpts.Inet4RouteExcludeAddress = linkLocalRanges
		}
		tunOpts.InterfaceFinder = b.finder
		tunOpts.InterfaceMonitor = b.ifMon
		finder = b.finder
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
		InterfaceFinder: finder,
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

// LUID identifies the virtual adapter (for firewall rules).
func (v *VPN) LUID() uint64 {
	if l, ok := v.tunIf.(interface{ LUID() uint64 }); ok {
		return l.LUID()
	}
	return 0
}

// Name is the adapter's interface name (e.g. utun5).
func (v *VPN) Name() string {
	n, _ := v.tunIf.Name()
	return n
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
		up, err = h.opts.Dial(ctx, target)
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
