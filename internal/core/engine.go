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
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	Status      string      `json:"status"`
	Mode        string      `json:"mode"`
	Error       string      `json:"error"`
	Server      *ServerInfo `json:"server"`
	ExitIP      string      `json:"exitIp"`
	ExitCountry string      `json:"exitCountry"`
	ConnectedAt int64       `json:"connectedAt"`
	QuotaLimit  int64       `json:"quotaLimit"`
	QuotaLeft   int64       `json:"quotaLeft"`
	ProxyAddr   string      `json:"proxyAddr"`
	// Account is the email whose proxy pass the connection is using.
	Account string `json:"account"`
}

type Engine struct {
	Auth *Auth

	mu        sync.Mutex
	settings  store.Settings
	servers   []mozilla.Country
	serversAt time.Time
	state     State

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
	passLimit  int64
	passLeft   int64
	passUpAt   int64
	passDownAt int64

	OnState  func(State)
	OnLog    func(string)
	OnNotice func(string)

	logMu   sync.Mutex
	logs    []string
	logFile *os.File
}

const maxLogFile = 2 << 20

func NewEngine() *Engine {
	// Undo a system proxy left behind by a crash or power loss.
	sysproxy.Restore()
	e := &Engine{Auth: NewAuth(), settings: store.LoadSettings(), renewKick: make(chan struct{}, 1)}
	netx.SetSplitMode(e.settings.TLSSplit)
	e.state = State{Status: StatusDisconnected, Mode: e.settings.Mode, Account: e.Auth.ActiveEmail()}
	e.openLogFile()
	return e
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

func (e *Engine) SaveSettings(s store.Settings) error {
	e.mu.Lock()
	e.settings = s
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
	return store.SaveSettings(s)
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
	up, err := tunnel.NewUpstream(ctx, ep.Host, ep.Port, pass.Token, nil)
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

// Servers returns the cached server list, refreshing it when stale.
func (e *Engine) Servers(force bool) ([]mozilla.Country, error) {
	e.mu.Lock()
	if !force && len(e.servers) > 0 && time.Since(e.serversAt) < 30*time.Minute {
		s := e.servers
		e.mu.Unlock()
		return s, nil
	}
	e.mu.Unlock()
	list, err := mozilla.FetchServerList()
	if err != nil {
		e.mu.Lock()
		s := e.servers
		e.mu.Unlock()
		if len(s) > 0 {
			return s, nil
		}
		// Mozilla's list server may be unreachable; the last saved copy
		// is good enough to connect.
		var cached []mozilla.Country
		if store.LoadJSON("servers.json", &cached) && len(cached) > 0 {
			e.Log("warning", "using the saved server list: "+err.Error())
			e.mu.Lock()
			e.servers = cached
			e.mu.Unlock()
			return cached, nil
		}
		e.Log("warning", "server list host unreachable, downloading it through the VPN: "+err.Error())
		if list, err = e.fetchServersViaTunnel(); err != nil {
			return nil, err
		}
	}
	store.SaveJSON("servers.json", list)
	e.mu.Lock()
	e.servers, e.serversAt = list, time.Now()
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
			ms := probeLatency(j.ep)
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
	e.done = make(chan struct{})
	e.mu.Unlock()

	e.update(func(s *State) {
		*s = State{Status: StatusConnecting, Mode: settings.Mode}
	})
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
				*s = State{Status: StatusDisconnected, Mode: settings.Mode, Error: msg, Account: e.Auth.ActiveEmail()}
			})
			close(e.done)
			return
		}
		go e.renewLoop(ctx)
		go e.healthLoop(ctx)
		go e.quotaPollLoop(ctx)
		go e.verifyExit(ctx)
	}()
}

func (e *Engine) connect(ctx context.Context, st store.Settings) error {
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
	for attempt := 1; err != nil && attempt < 3 && !switchable(err) && !errors.Is(err, ErrNotLoggedIn) && ctx.Err() == nil; attempt++ {
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
	e.Log("info", fmt.Sprintf("using account %s (%s left)", email, quotaText(pass.QuotaLeft)))
	e.applyPass(pass, email)

	countries, err := e.Servers(false)
	if err != nil {
		return err
	}
	ep, err := mozilla.PickEndpoint(countries, st.CountryCode, st.CityCode)
	if err != nil {
		return err
	}
	e.Log("info", fmt.Sprintf("selected upstream proxy %s (%s)", ep.Addr(), ep.CountryCode))

	var tun TunDriver
	if st.Mode == store.ModeTUN {
		tun = NewTunDriver()
		e.mu.Lock()
		e.tun = tun
		e.mu.Unlock()
		if err := tun.Prepare(); err != nil {
			return err
		}
	}

	up, err := tunnel.NewUpstream(ctx, ep.Host, ep.Port, pass.Token, e.onRejected)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.up = up
	e.mu.Unlock()
	if err := up.Warmup(ctx); err != nil {
		return err
	}
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
		onFail := func(err error) {
			e.Log("error", err.Error())
			e.notify("Full VPN stopped: " + err.Error())
			go e.Disconnect()
		}
		cfg := TunConfig{
			Dial: up.Dial, Forward: forward, ProxyAddr: mixed.Addr(),
			BypassIran: st.BypassIran, BypassLAN: st.BypassLAN,
			Protect: up.Addrs(), ProtectPort: ep.Port,
			AppTunnelMode: st.AppTunnelMode, Apps: st.Apps,
			Log: e.logRaw, OnFail: onFail,
		}
		if err := tun.Start(cfg); err != nil {
			return err
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

func (e *Engine) verifyExit(ctx context.Context) {
	e.mu.Lock()
	up := e.up
	e.mu.Unlock()
	if up == nil {
		return
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			Proxy:       nil,
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) { return up.Dial(ctx, addr) },
		},
	}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://www.cloudflare.com/cdn-cgi/trace", nil)
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			e.Log("warning", "exit verification failed (non-fatal): "+err.Error())
		}
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
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
	if ip == "" {
		return
	}
	e.Log("info", fmt.Sprintf("exit verification: exit ip %s, country %s", ip, loc))
	e.update(func(s *State) {
		if s.Status == StatusConnected {
			s.ExitIP, s.ExitCountry = ip, loc
		}
	})
}

// SetError shows a message in the banner (web sign-in failures land here,
// since the app page has just reloaded and missed the live events).
func (e *Engine) SetError(msg string) {
	e.update(func(s *State) { s.Error = msg })
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
func (e *Engine) LiveQuota() (limit, left int64) {
	e.mu.Lock()
	limit, left, upAt, downAt := e.passLimit, e.passLeft, e.passUpAt, e.passDownAt
	u := e.up
	e.mu.Unlock()
	if left > 0 && u != nil {
		if used := u.Up.Load() - upAt + u.Down.Load() - downAt; used > 0 {
			left = max(left-used, 0)
		}
	}
	return limit, left
}

func (e *Engine) teardown() {
	if killswitch.Active() {
		killswitch.Disable()
		e.Log("info", "kill switch off: internet unblocked")
	}
	e.mu.Lock()
	tun, mixed, up := e.tun, e.mixed, e.up
	e.tun, e.mixed, e.up = nil, nil, nil
	e.mu.Unlock()
	mozilla.SetTunnel(nil)
	if tun != nil {
		tun.Close()
	}
	sysproxy.Restore()
	if mixed != nil {
		mixed.Close()
	}
	if up != nil {
		up.Close()
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
