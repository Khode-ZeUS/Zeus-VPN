// Package core orchestrates sign-in, server selection and the connection
// lifecycle for the UI.
package core

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zeusvpn/internal/killswitch"
	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/netx"
	"zeusvpn/internal/store"
	"zeusvpn/internal/sysproxy"
	"zeusvpn/internal/tunnel"
)

const (
	StatusDisconnected  = "disconnected"
	StatusConnecting    = "connecting"
	StatusConnected     = "connected"
	StatusDisconnecting = "disconnecting"
	// StatusReconnecting means the tunnel stopped working and is being
	// restored; the kill switch, if enabled, blocks traffic meanwhile.
	StatusReconnecting = "reconnecting"
)

type ServerInfo struct {
	CountryCode string `json:"countryCode"`
	CountryName string `json:"countryName"`
	CityName    string `json:"cityName"`
	Host        string `json:"host"`
}

type State struct {
	Status                   string      `json:"status"`
	Mode                     string      `json:"mode"`
	Error                    string      `json:"error"`
	NeedsBrowserVerification bool        `json:"needsBrowserVerification,omitempty"`
	Server                   *ServerInfo `json:"server"`
	ExitIP                   string      `json:"exitIp"`
	ExitCountry              string      `json:"exitCountry"`
	ConnectedAt              int64       `json:"connectedAt"`
	QuotaLimit               int64       `json:"quotaLimit"`
	QuotaLeft                int64       `json:"quotaLeft"`
	ProxyAddr                string      `json:"proxyAddr"`
	// Account is the email whose proxy pass the connection is using.
	Account string `json:"account"`
}

type Engine struct {
	Auth *Auth

	mu           sync.Mutex
	settings     store.Settings
	servers      []mozilla.Country
	serversAt    time.Time
	state        State
	boot         atomic.Pointer[mozilla.Bootstrap]
	relay        *netx.Relay
	relayErr     error
	managedRelay string

	up     *tunnel.Upstream
	mixed  *tunnel.MixedServer
	tun    TunDriver
	cancel context.CancelFunc
	done   chan struct{}

	passMu      sync.Mutex
	passExpires time.Time
	renewKick   chan struct{}

	// Quota as of the moment the active pass was issued, with the tunnel's
	// traffic counters at that moment: the server only reports its numbers
	// when a pass is fetched, so LiveQuota subtracts the traffic the
	// connection has moved since (other clients of the same account are
	// picked up by the next server refresh).
	quota quotaEstimate

	OnState  func(State)
	OnLog    func(string)
	OnNotice func(string)

	logMu   sync.Mutex
	logs    []string
	logFile *os.File
}

const maxLogFile = 2 << 20

func NewEngine() *Engine {
	return NewEngineWithManagedRelay("")
}

func NewEngineWithManagedRelay(endpoint string) *Engine {
	// Undo a system proxy left behind by a crash or power loss.
	sysproxy.Restore()
	e := &Engine{Auth: NewAuth(), settings: store.LoadSettings(), managedRelay: endpoint, renewKick: make(chan struct{}, 1)}
	netx.SetSplitMode(e.settings.TLSSplit)
	e.state = State{Status: StatusDisconnected, Mode: e.settings.Mode, Account: e.Auth.ActiveEmail()}
	e.relay, e.relayErr = e.relayFor(e.settings)
	e.configureRelay()
	e.rebuildBootstrap()
	e.openLogFile()
	return e
}

// rebuildBootstrap (re)creates the server-list bootstrap manager with the
// current settings: it picks up a user-configured upstream proxy and the
// per-network memory of which path worked.
func (e *Engine) rebuildBootstrap() {
	e.boot.Store(mozilla.NewBootstrap(mozilla.BootstrapOptions{
		ProxyURL:  e.settings.UpstreamProxy,
		RelayOnly: e.relay != nil || e.relayErr != nil,
		Masque:    e.fetchServersViaTunnel,
		Cache: func() ([]mozilla.Country, bool) {
			var cached []mozilla.Country
			return cached, store.LoadJSON("servers.json", &cached) && len(cached) > 0
		},
	}))
}

func (e *Engine) configureRelay() {
	mozilla.SetFirefoxHTTPRelay(e.relay.FirefoxAccountsEndpoint(), e.relay.FirefoxAccountsTransport())
	switch {
	case e.relayErr != nil:
		err := e.relayErr
		mozilla.SetRelay(func(context.Context, string, string) (net.Conn, error) { return nil, err })
	case e.relay != nil:
		mozilla.SetRelay(e.relay.DialContext)
	default:
		mozilla.SetRelay(nil)
	}
}

// LogPath is the persistent log, kept for troubleshooting.
func LogPath() string { return filepath.Join(store.Dir(), "zeusvpn.log") }

func (e *Engine) openLogFile() {
	p := LogPath()
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogFile {
		os.Rename(p, p+".old")
	}
	if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		e.logFile = f
	}
}

func (e *Engine) Log(level, msg string) {
	line := time.Now().Format("2006-01-02 15:04:05") + " " + level + "  " + msg
	e.logMu.Lock()
	e.logs = append(e.logs, line)
	if len(e.logs) > 500 {
		e.logs = e.logs[len(e.logs)-500:]
	}
	if e.logFile != nil {
		e.logFile.WriteString(line + "\r\n")
	}
	e.logMu.Unlock()
	if e.OnLog != nil {
		e.OnLog(line)
	}
}

// notify shows a short message to the user (e.g. an account switch).
func (e *Engine) notify(msg string) {
	if e.OnNotice != nil {
		e.OnNotice(msg)
	}
}

func (e *Engine) logRaw(s string) {
	level, msg, _ := strings.Cut(s, " ")
	e.Log(level, msg)
}

func (e *Engine) Logs() []string {
	e.logMu.Lock()
	defer e.logMu.Unlock()
	return append([]string(nil), e.logs...)
}

func (e *Engine) State() State {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *Engine) update(f func(s *State)) {
	e.mu.Lock()
	f(&e.state)
	s := e.state
	e.mu.Unlock()
	if e.OnState != nil {
		e.OnState(s)
	}
}

func (e *Engine) Settings() store.Settings {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.settings
}

// ConnectionRelay returns the relay used for Mozilla APIs and web sign-in.
func (e *Engine) ConnectionRelay() (*netx.Relay, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.relay, e.relayErr
}

func (e *Engine) relayFor(s store.Settings) (*netx.Relay, error) {
	if s.RelayProxy == "" && e.managedRelay != "" {
		return netx.NewManagedRelay(e.managedRelay)
	}
	return netx.NewRelay(s.RelayProxy, s.ProxyPort)
}

func (e *Engine) SaveSettings(s store.Settings) error {
	s.RelayProxy = strings.TrimSpace(s.RelayProxy)
	e.mu.Lock()
	relayChanged := e.settings.RelayProxy != s.RelayProxy
	if relayChanged && e.state.Status != StatusDisconnected {
		e.mu.Unlock()
		return errors.New("disconnect the VPN before changing the relay")
	}
	var relay *netx.Relay
	if relayChanged || s.ProxyPort != e.settings.ProxyPort {
		var err error
		relay, err = e.relayFor(s)
		if err != nil {
			e.mu.Unlock()
			return err
		}
	}
	if err := store.SaveSettings(s); err != nil {
		e.mu.Unlock()
		return err
	}
	proxyChanged := e.settings.UpstreamProxy != s.UpstreamProxy
	e.settings = s
	if relayChanged {
		e.relay, e.relayErr = relay, nil
		e.configureRelay()
	}
	if proxyChanged || relayChanged {
		e.rebuildBootstrap()
	}
	e.mu.Unlock()
	netx.SetSplitMode(s.TLSSplit)
	if !s.KillSwitch && killswitch.Active() {
		killswitch.Disable()
		e.Log("info", "kill switch turned off: internet unblocked")
	}
	e.update(func(st *State) {
		if st.Status == StatusDisconnected {
			st.Mode = s.Mode
		}
	})
	return nil
}

// ResetNetworkConfig forgets the learned network choices (which bootstrap
// path worked where) — Reset Network Configuration in the settings.
func (e *Engine) ResetNetworkConfig() {
	e.boot.Load().Reset()
}

// fetchServersViaTunnel downloads the server list through Firefox's
// recommended server, for networks that block the list host.
func (e *Engine) fetchServersViaTunnel() ([]mozilla.Country, error) {
	pass, err := e.passFor(e.Auth.ActiveEmail())
	if err != nil {
		return nil, fmt.Errorf("could not download the server list: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ep := mozilla.BootstrapEndpoint
	e.mu.Lock()
	relay, relayErr := e.relay, e.relayErr
	e.mu.Unlock()
	if relayErr != nil {
		return nil, relayErr
	}
	up, err := tunnel.NewRelayedUpstream(ctx, ep.Host, ep.Port, pass.Token, nil, relay.TunnelRelay())
	if err != nil {
		return nil, err
	}
	defer up.Close()
	tr := &http.Transport{
		Proxy:             nil,
		DialContext:       func(ctx context.Context, _, addr string) (net.Conn, error) { return up.Dial(ctx, addr) },
		ForceAttemptHTTP2: true,
	}
	defer tr.CloseIdleConnections()
	list, err := mozilla.FetchServerListWith(&http.Client{Transport: tr, Timeout: 30 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("could not download the server list: %w", err)
	}
	return list, nil
}

// Servers returns the cached server list, refreshing it when stale. The
// refresh goes through the bootstrap manager: direct, the user's upstream
// proxy, Firefox's recommended masque server, or the last saved copy —
// whichever works first, remembered per network.
func (e *Engine) Servers(force bool) ([]mozilla.Country, error) {
	e.mu.Lock()
	if !force && len(e.servers) > 0 && time.Since(e.serversAt) < 30*time.Minute {
		s := e.servers
		e.mu.Unlock()
		return s, nil
	}
	e.mu.Unlock()
	list, reports, err := e.boot.Load().ServerList()
	for _, r := range reports {
		if r.OK {
			e.Log("info", fmt.Sprintf("server list via %s (%d ms)", r.Path, r.Millis))
		} else {
			e.Log("warning", "bootstrap path "+r.Path+" failed: "+r.Err)
		}
	}
	if err != nil {
		// Every path failed: Mozilla's recommended server itself stays
		// reachable as a last resort, so a connect is always possible —
		// the health machinery and failover take it from there. The
		// single-server fallback must NOT overwrite the saved list: the
		// next refresh retries in a few minutes instead of a half hour.
		list = mozilla.FallbackServerList()
		e.Log("warning", "no bootstrap path worked, falling back to the built-in recommended server")
	} else {
		store.SaveJSON("servers.json", list)
	}
	e.mu.Lock()
	e.servers = list
	if err != nil {
		e.serversAt = time.Now().Add(-25 * time.Minute) // retry soon
	} else {
		e.serversAt = time.Now()
	}
	e.mu.Unlock()
	return list, nil
}

// Latency measures the TCP + TLS handshake time (ms) to one server per
// city; -1 = unreachable. The handshake must succeed: filters that answer
// the TCP SYN themselves would otherwise show a fake single-digit ping.
func (e *Engine) Latency() map[string]int {
	countries, err := e.Servers(false)
	out := map[string]int{}
	if err != nil {
		return out
	}
	e.mu.Lock()
	relay, relayErr := e.relay, e.relayErr
	e.mu.Unlock()
	relay = relay.TunnelRelay()
	type job struct {
		key string
		ep  mozilla.Endpoint
	}
	var jobs []job
	for _, c := range countries {
		for _, city := range c.Cities {
			if cands := mozilla.Candidates(countries, c.Code, city.Code); len(cands) > 0 {
				jobs = append(jobs, job{c.Code + "/" + city.Code, cands[0]})
			}
		}
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 24)
	for _, j := range jobs {
		wg.Add(1)
		sem <- struct{}{}
		go func(j job) {
			defer wg.Done()
			defer func() { <-sem }()
			ms := -1
			switch {
			case relay != nil:
				ms = probeRelayLatency(j.ep, relay)
			case relayErr == nil:
				ms = probeLatency(j.ep)
			}
			if ms > 0 {
				mozilla.MarkEndpointOK(j.ep.Addr(), int64(ms))
			} else {
				mozilla.MarkEndpointFail(j.ep.Addr())
			}
			mu.Lock()
			out[j.key] = ms
			mu.Unlock()
		}(j)
	}
	wg.Wait()
	return out
}

// probeBudget bounds one latency probe, including a fragmented-handshake
// retry. It is generous because filtered networks resolve and handshake
// slowly, and the probe must not time out while a real connection would
// succeed.
const probeBudget = 12 * time.Second

func probeRelayLatency(ep mozilla.Endpoint, relay *netx.Relay) int {
	ctx, cancel := context.WithTimeout(context.Background(), probeBudget)
	defer cancel()
	start := time.Now()
	raw, err := relay.DialContext(ctx, "tcp", ep.Addr())
	if err != nil {
		return -1
	}
	defer raw.Close()
	tc := tls.Client(raw, &tls.Config{ServerName: ep.Host, NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS12})
	if err := tc.HandshakeContext(ctx); err != nil {
		return -1
	}
	return max(1, int(time.Since(start).Milliseconds()))
}

// probeLatency measures a full TCP + TLS handshake (the same path a real
// connection takes, TLS ClientHello fragmentation included) and reports the
// elapsed milliseconds, or -1 when the server cannot be reached. DNS is
// resolved beforehand and not part of the measured time.
func probeLatency(ep mozilla.Endpoint) int {
	host, portStr, err := net.SplitHostPort(ep.Addr())
	if err != nil {
		return -1
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeBudget)
	defer cancel()
	ips, err := netx.LookupHost(ctx, host)
	if err != nil || len(ips) == 0 {
		return -1
	}
	// Addresses are raced, each 300 ms after the previous one: some
	// addresses of a server can be blackholed while others answer (the
	// same trick netx.DialResolved applies to real connections).
	type attempt struct {
		ms   int
		conn net.Conn
	}
	results := make(chan attempt, len(ips))
	for i, ip := range ips {
		go func(i int, ip netip.Addr) {
			select {
			case <-time.After(time.Duration(i) * 300 * time.Millisecond):
			case <-ctx.Done():
				results <- attempt{0, nil}
				return
			}
			ms, conn, err := probeHandshake(ctx, host, ip.String(), portStr)
			if err == nil {
				results <- attempt{ms, conn}
			} else {
				results <- attempt{0, nil}
			}
		}(i, ip)
	}
	var ms = -1
	var live net.Conn
	for landed := 0; landed < len(ips); landed++ {
		select {
		case a := <-results:
			if a.conn != nil {
				if live == nil {
					ms, live = a.ms, a.conn
				} else {
					a.conn.Close() // first success wins; close the rest
				}
			}
		case <-ctx.Done():
			landed = len(ips) // the rest fail on their own and close themselves
		}
	}
	if live != nil {
		live.Close()
	}
	return ms
}

// probeHandshake opens one TCP connection and completes the TLS handshake,
// measuring the elapsed time. If the plain attempt is cut off (filtered
// server, e.g. Canada) it is retried with the ClientHello fragmented
// (SplitSNI) — exactly what Upstream.newConn does for real connections — so
// pings do not time out while connections succeed. DNS is resolved by the
// caller and is not part of the measured time.
func probeHandshake(ctx context.Context, host, ipStr, portStr string) (int, net.Conn, error) {
	start := time.Now()
	dialAndShake := func() (net.Conn, error) {
		conn, err := netx.DialContext(ctx, "tcp", net.JoinHostPort(ipStr, portStr))
		if err != nil {
			return nil, err
		}
		tc := tls.Client(conn, &tls.Config{ServerName: host, NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS12})
		if err := tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, err
		}
		return tc, nil
	}
	conn, err := dialAndShake()
	if err == nil {
		return int(time.Since(start).Milliseconds()), conn, nil
	}
	// The plain attempt failed (dial or TLS handshake). Mark the host so the
	// retry uses ClientHello fragmentation, then try once more.
	netx.MarkBlocked(host)
	if ctx.Err() != nil {
		return 0, nil, err
	}
	conn, err = dialAndShake()
	if err != nil {
		return 0, nil, err
	}
	return int(time.Since(start).Milliseconds()), conn, nil
}

// Connect starts the connection in the background; progress arrives via OnState.
func (e *Engine) Connect() {
	e.mu.Lock()
	if e.state.Status != StatusDisconnected {
		e.mu.Unlock()
		return
	}
	settings := e.settings
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	done := make(chan struct{})
	e.done = done
	e.state = State{Status: StatusConnecting, Mode: settings.Mode}
	connecting := e.state
	e.mu.Unlock()

	if e.OnState != nil {
		e.OnState(connecting)
	}
	go func() {
		err := e.connect(ctx, settings)
		if err != nil {
			if ctx.Err() == nil {
				e.Log("error", err.Error())
			}
			e.teardown()
			msg := err.Error()
			if ctx.Err() != nil {
				msg = ""
			}
			e.update(func(s *State) {
				*s = State{Status: StatusDisconnected, Mode: settings.Mode, Error: msg, Account: e.Auth.ActiveEmail(),
					NeedsBrowserVerification: msg != "" && errors.Is(err, mozilla.ErrInteractiveChallenge)}
			})
			close(done)
			return
		}
		go e.renewLoop(ctx)
		go e.healthLoop(ctx)
		go e.quotaPollLoop(ctx)
	}()
}

func (e *Engine) connect(ctx context.Context, st store.Settings) error {
	relay, err := e.ConnectionRelay()
	if err != nil {
		return err
	}
	// A reconnect can arrive while the previous session's pieces are still
	// up (mobile: the tunnel dropped and the host re-entered connect) —
	// close the stale ones instead of failing later on a busy port.
	e.mu.Lock()
	staleMixed, staleUp := e.mixed, e.up
	e.mixed, e.up = nil, nil
	e.mu.Unlock()
	if staleMixed != nil {
		staleMixed.Close()
	}
	if staleUp != nil {
		staleUp.Close()
	}
	if st.Mode == store.ModeTUN && TunNeedsElevation && !IsElevated() {
		return errors.New("full VPN mode needs administrator rights")
	}
	if st.KillSwitch && killswitch.Supported && !IsElevated() {
		return errors.New("the kill switch needs administrator rights")
	}
	if !e.Auth.LoggedIn() {
		return ErrNotLoggedIn
	}
	e.Log("info", fmt.Sprintf("requesting proxy pass from Mozilla (TLS fragmentation: %s)", netx.SplitMode()))
	pass, email, err := e.acquirePass(st.AutoSwitch)
	// Mozilla's API hosts are intermittently unreachable; retry briefly
	// before giving up on network errors.
	for attempt := 1; err != nil && attempt < 3 && !switchable(err) && !errors.Is(err, ErrNotLoggedIn) && !errors.Is(err, mozilla.ErrInteractiveChallenge) && ctx.Err() == nil; attempt++ {
		e.Log("warning", "could not get a proxy pass, retrying: "+err.Error())
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt) * 3 * time.Second):
		}
		pass, email, err = e.acquirePass(st.AutoSwitch)
	}
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.Log("info", fmt.Sprintf("using account %s (%s left)", email, quotaText(pass.QuotaLeft)))
	e.applyPass(pass, email)

	countries, err := e.Servers(false)
	if err != nil {
		return err
	}
	// "Recommended" is stored as an empty country: it means the REC
	// pseudo-country of Mozilla's list (the old PickEndpoint mapped it).
	cc := st.CountryCode
	if cc == "" {
		cc = mozilla.RecommendedCode
	}
	cands := mozilla.OrderHealthy(mozilla.Candidates(countries, cc, st.CityCode))
	if len(cands) == 0 && st.CityCode != "" {
		cands = mozilla.OrderHealthy(mozilla.Candidates(countries, cc, ""))
	}
	if len(cands) == 0 {
		return fmt.Errorf("no available servers for %s", cc)
	}

	var tun TunDriver
	if st.Mode == store.ModeTUN {
		tun = NewTunDriver()
		e.mu.Lock()
		e.tun = tun
		e.mu.Unlock()
		if err := tun.Prepare(); err != nil {
			return err
		}
		if relay.WorkerHost() != "" {
			binder, ok := tun.(relayEdgeBinder)
			if !ok {
				return errors.New("the Full VPN adapter cannot route the Worker relay")
			}
			if err := relay.BindWorker(ctx, binder.RelayEdgeDialer()); err != nil {
				return err
			}
		}
	}

	// Try up to three servers in the selected location: on networks that hijack the
	// VPN-server handshake (a forged certificate), the first endpoint may
	// be unreachable while the next one is not.
	tried := 0
	seen := map[string]bool{}
	var up *tunnel.Upstream
	var ep mozilla.Endpoint
	var exitIP, exitCountry string
	var lastErr error
	tunnelRelay := relay.TunnelRelay()
	if relay != nil && tunnelRelay == nil {
		e.Log("info", "Mozilla API via Worker; VPN traffic via direct upstream")
	}
	attempt := func(cand mozilla.Endpoint) bool {
		if tried >= 5 || seen[cand.Addr()] || ctx.Err() != nil {
			return false
		}
		seen[cand.Addr()] = true
		tried++
		e.Log("info", fmt.Sprintf("trying upstream proxy %s (%s)", cand.Addr(), cand.CountryCode))
		candidate, err := tunnel.NewRelayedUpstream(ctx, cand.Host, cand.Port, pass.Token, e.onRejected, tunnelRelay)
		lastErr = err
		if lastErr != nil {
			mozilla.MarkEndpointFail(cand.Addr())
			return false
		}
		// Warmup only establishes the HTTP/2 connection — a middlebox can
		// let the handshake through and drop the payload, which the user
		// sees as 'connected but nothing works'. Verify a real end-to-end
		// stream before declaring the connect.
		if lastErr = candidate.Warmup(ctx); lastErr != nil {
			lastErr = fmt.Errorf("upstream HTTP/2 setup: %w", lastErr)
		} else {
			exitIP, exitCountry, lastErr = e.probeConnection(ctx, candidate)
		}
		if lastErr != nil {
			candidate.Close()
			if ctx.Err() == nil {
				mozilla.MarkEndpointFail(cand.Addr())
				e.Log("warning", "server "+cand.Addr()+" did not carry traffic: "+lastErr.Error())
			}
			return false
		}
		up, ep = candidate, cand
		mozilla.MarkEndpointOK(cand.Addr(), 0)
		return true
	}
	for _, cand := range cands {
		if ctx.Err() != nil || tried >= 3 || attempt(cand) {
			break
		}
	}
	if up == nil {
		// The chosen location's endpoints all failed — on some networks
		// every address of the recommended record is hijacked. Widen the
		// search to every country's servers: a working exit anywhere beats
		// no connection.
		var wide []mozilla.Endpoint
		for i := range countries {
			wide = append(wide, mozilla.Candidates(countries, countries[i].Code, "")...)
		}
		wide = mozilla.OrderHealthy(wide)
		for _, cand := range wide {
			if ctx.Err() != nil || tried >= 5 || attempt(cand) {
				break
			}
		}
	}
	if up == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		if lastErr == nil {
			lastErr = errors.New("no VPN server accepted the connection")
		}
		return lastErr
	}
	e.mu.Lock()
	e.up = up
	e.mu.Unlock()
	// From now on Mozilla's API can also be reached through the tunnel.
	mozilla.SetTunnel(up.Dial)
	e.Log("info", "upstream transport=http2")

	dial, forward := e.routes(up, st)
	mixed, err := tunnel.ListenMixed(net.JoinHostPort("127.0.0.1", strconv.Itoa(st.ProxyPort)), dial, forward, e.logRaw)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.mixed = mixed
	e.mu.Unlock()
	e.Log("info", "local proxy (HTTP + SOCKS5) listening on "+mixed.Addr())

	if st.Mode == store.ModeTUN {
		protect, protectPort := up.SocketRoute()
		onFail := func(err error) {
			e.Log("error", err.Error())
			e.notify("Full VPN stopped: " + err.Error())
			go e.Disconnect()
		}
		cfg := TunConfig{
			Dial: up.Dial, Forward: forward, ProxyAddr: mixed.Addr(),
			BypassIran: st.BypassIran, BypassLAN: st.BypassLAN,
			Protect: protect, ProtectPort: protectPort,
			WorkerHost:    relay.WorkerHost(),
			AppTunnelMode: st.AppTunnelMode, Apps: st.Apps,
			Log: e.logRaw, OnFail: onFail,
		}
		if err := tun.Start(cfg); err != nil {
			return err
		}
		if relay.WorkerHost() != "" {
			// Warmup opened a socket before the routes changed. Confirm that a
			// new Worker socket can also open, as it will need to on reconnect.
			conn, err := relay.DialContext(ctx, "tcp", "vpn.mozilla.org:443")
			if err != nil {
				return fmt.Errorf("the Worker relay could not reconnect outside the Full VPN adapter: %w", err)
			}
			conn.Close()
		}
		if cfg.AppTunnelMode == store.AppTunnelInclude {
			e.Log("info", fmt.Sprintf("full VPN mode active: tunnelling only the selected apps (%d)", len(cfg.Apps)))
		} else if cfg.AppTunnelMode == store.AppTunnelExclude {
			e.Log("info", fmt.Sprintf("full VPN mode active: all traffic is tunneled except the selected apps (%d)", len(cfg.Apps)))
		} else {
			e.Log("info", "full VPN mode active: all system traffic is tunneled")
		}
	} else {
		if err := sysproxy.Enable(mixed.Addr(), st.BypassIran, st.BypassLAN); err != nil {
			return fmt.Errorf("could not set the system proxy: %w", err)
		}
		e.Log("info", "system proxy set to "+mixed.Addr())
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}
	e.update(func(s *State) {
		s.Status = StatusConnected
		s.Error = ""
		s.ConnectedAt = time.Now().UnixMilli()
		s.Server = &ServerInfo{CountryCode: ep.CountryCode, CountryName: ep.CountryName, CityName: ep.CityName, Host: ep.Host}
		s.ProxyAddr = mixed.Addr()
		s.ExitIP, s.ExitCountry = exitIP, exitCountry
	})
	return nil
}

func isIranDomain(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == "ir" || strings.HasSuffix(host, ".ir")
}

// routes returns how to reach a destination: through the tunnel, or
// directly for .ir domains when Iranian sites are bypassed (many of them
// refuse foreign IPs).
func (e *Engine) routes(up *tunnel.Upstream, st store.Settings) (tunnel.DialFunc, tunnel.RoundTripFunc) {
	direct := &http.Transport{Proxy: nil, DialContext: netx.DialContext, DisableCompression: true, IdleConnTimeout: 30 * time.Second}
	dial := func(ctx context.Context, target string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(target)
		if st.BypassIran && isIranDomain(host) {
			return netx.DialContext(ctx, "tcp", target)
		}
		return up.Dial(ctx, target)
	}
	forward := func(r *http.Request) (*http.Response, error) {
		if st.BypassIran && isIranDomain(r.URL.Hostname()) {
			return direct.RoundTrip(r)
		}
		return up.RoundTrip(r)
	}
	return dial, forward
}

const exitProbeURL = "https://www.cloudflare.com/cdn-cgi/trace"

type probeTraffic struct {
	net.Conn
	sent, received atomic.Int64
}

func (c *probeTraffic) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.sent.Add(int64(n))
	return n, err
}

func (c *probeTraffic) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.received.Add(int64(n))
	return n, err
}

func (e *Engine) probeConnection(ctx context.Context, up *tunnel.Upstream) (string, string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	targets := []string{exitProbeURL, "https://www.gstatic.com/generate_204", "https://example.com/"}
	type result struct {
		ip, country string
		err         error
	}
	results := make(chan result, len(targets))
	for _, target := range targets {
		go func(target string) {
			ip, country, err := probeExit(ctx, up, target, nil)
			results <- result{ip, country, err}
		}(target)
	}
	var failures []error
	for range targets {
		select {
		case <-ctx.Done():
			return "", "", ctx.Err()
		case r := <-results:
			if r.err == nil {
				return r.ip, r.country, nil
			}
			failures = append(failures, r.err)
		}
	}
	return "", "", errors.Join(failures...)
}

func probeExit(ctx context.Context, up *tunnel.Upstream, target string, report func(string)) (string, string, error) {
	start := time.Now()
	var traffic atomic.Pointer[probeTraffic]
	var connectAt, tlsAt, sentAt atomic.Int64
	trace := &httptrace.ClientTrace{
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) {
			if err == nil {
				tlsAt.Store(time.Since(start).Nanoseconds())
			}
		},
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				sentAt.Store(time.Since(start).Nanoseconds())
			}
		},
	}
	client := &http.Client{
		Timeout:       10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				// Keep the destination trace separate from the upstream HTTP/2 CONNECT.
				dialCtx, cancel := context.WithDeadline(context.Background(), start.Add(10*time.Second))
				stop := context.AfterFunc(ctx, cancel)
				defer stop()
				defer cancel()
				conn, err := up.Dial(dialCtx, addr)
				if err == nil {
					connectAt.Store(time.Since(start).Nanoseconds())
					stream := &probeTraffic{Conn: conn}
					traffic.Store(stream)
					return stream, nil
				}
				return conn, err
			},
		},
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), "GET", target, nil)
	resp, err := client.Do(req)
	headersAt := time.Since(start)
	connected, secured, sent := time.Duration(connectAt.Load()), time.Duration(tlsAt.Load()), time.Duration(sentAt.Load())
	if report != nil {
		defer func() {
			var tx, rx int64
			if stream := traffic.Load(); stream != nil {
				tx, rx = stream.sent.Load(), stream.received.Load()
			}
			report(fmt.Sprintf("CONNECT at %s, TLS at %s, request at %s, total %s, tunnel bytes sent=%d received=%d (0ms means stage not observed)",
				connected.Round(time.Millisecond), secured.Round(time.Millisecond), sent.Round(time.Millisecond),
				time.Since(start).Round(time.Millisecond), tx, rx))
		}()
	}
	if err != nil {
		stage := "VPN CONNECT"
		switch {
		case sent > 0:
			stage = "HTTPS response headers"
		case secured > 0:
			stage = "HTTPS request write"
		case connected > 0:
			stage = "destination TLS"
		}
		return "", "", fmt.Errorf("VPN probe failed at %s after %s: %w", stage, headersAt.Round(time.Millisecond), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && !(target != exitProbeURL && resp.StatusCode == http.StatusNoContent) {
		return "", "", fmt.Errorf("VPN exit probe returned HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return "", "", fmt.Errorf("reading VPN exit probe: %w", err)
	}
	if target != exitProbeURL {
		return "", "", nil
	}
	var ip, loc string
	for _, line := range strings.Split(string(body), "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(line), "=")
		switch k {
		case "ip":
			ip = v
		case "loc":
			loc = strings.ToUpper(v)
		}
	}
	if net.ParseIP(ip) == nil {
		return "", "", errors.New("VPN exit probe did not return a valid IP")
	}
	return ip, loc, nil
}

// SetError shows a message in the banner (web sign-in failures land here,
// since the app page has just reloaded and missed the live events).
func (e *Engine) SetError(msg string) {
	e.update(func(s *State) { s.Error, s.NeedsBrowserVerification = msg, false })
}

// Traffic returns total bytes sent and received through the tunnel.
func (e *Engine) Traffic() (up, down int64) {
	e.mu.Lock()
	u := e.up
	e.mu.Unlock()
	if u == nil {
		return 0, 0
	}
	return u.Up.Load(), u.Down.Load()
}

// LiveQuota estimates the remaining monthly data of the account in use.
// The server's numbers are only re-read every few minutes (with each proxy
// pass), so the bytes this connection has moved since the last fetch are
// subtracted here: the counter counts down live instead of staying frozen
// at the value of the last renewal.
func (e *Engine) LiveQuota() (account string, limit, left int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.up == nil {
		return "", 0, 0
	}
	return e.quota.account, e.quota.limit, e.quota.remaining(tunnelBytes(e.up))
}

func (e *Engine) teardown() {
	if killswitch.Active() {
		killswitch.Disable()
		e.Log("info", "kill switch off: internet unblocked")
	}
	e.mu.Lock()
	tun, mixed, up, relay := e.tun, e.mixed, e.up, e.relay
	quota := e.quota
	e.tun, e.mixed, e.up = nil, nil, nil
	e.quota = quotaEstimate{}
	e.mu.Unlock()
	mozilla.SetTunnel(nil)
	if tun != nil {
		tun.Close()
		relay.UnbindWorker()
	}
	sysproxy.Restore()
	if mixed != nil {
		mixed.Close()
	}
	if up != nil {
		up.Close()
		e.Auth.saveLocalQuota(quota, tunnelBytes(up))
	}
}

// Disconnect stops the connection and waits for cleanup.
func (e *Engine) Disconnect() {
	e.mu.Lock()
	if e.state.Status == StatusDisconnected || e.cancel == nil {
		e.mu.Unlock()
		return
	}
	cancel, done := e.cancel, e.done
	e.cancel = nil
	e.mu.Unlock()
	e.update(func(s *State) { s.Status = StatusDisconnecting })
	cancel()
	e.teardown()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	e.Log("info", "disconnected")
	mode := e.Settings().Mode
	e.update(func(s *State) { *s = State{Status: StatusDisconnected, Mode: mode, Account: e.Auth.ActiveEmail()} })
}
