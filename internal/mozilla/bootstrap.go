package mozilla

import (
	"errors"
	"net/http"
	"sync"
	"time"

	"zeusvpn/internal/netdiag"
	"zeusvpn/internal/netx"
	"zeusvpn/internal/store"
)

// Bootstrap downloads the server list over whichever independent path
// works: the Mozilla settings host directly, the user's own proxy, or
// Firefox's recommended masque server — with the last saved copy as the
// final fallback. What worked is remembered per network, so on a network
// where the direct path is refused the app starts with the path that
// succeeded there instead of making the user wait through the failures.

const bootstrapStateFile = "bootstrap.json"

// badPathCooldown is how long a path that failed on this network sits out.
const badPathCooldown = 30 * time.Minute

// bootstrapPathTimeout bounds one path attempt: a hanging path must not
// stall the whole bootstrap (the goroutine is abandoned when it runs late).
const bootstrapPathTimeout = 12 * time.Second

// raceStagger is the head start each earlier path gets in the parallel
// bootstrap: the preferred path usually answers within 2s.
const raceStagger = 2 * time.Second

func fetchWithTimeout(p namedPath, d time.Duration) (list []Country, err error) {
	type result struct {
		list []Country
		err  error
	}
	done := make(chan result, 1)
	go func() {
		l, e := p.fetch()
		done <- result{l, e}
	}()
	select {
	case r := <-done:
		return r.list, r.err
	case <-time.After(d):
		return nil, errors.New("timed out")
	}
}

// BootstrapOptions wires the environment into the manager. Masque and Cache
// may be nil: Masque needs a signed-in account (it tunnels through
// Firefox's recommended server), Cache reads the last saved list.
type BootstrapOptions struct {
	// ProxyURL is the user's own upstream proxy ("socks5://…" /
	// "http(s)://…"), or "" when none is configured.
	ProxyURL string
	// Sig returns the current network's signature ("": unknown).
	Sig func() string
	// Masque fetches the list through Firefox's recommended masque server.
	Masque func() ([]Country, error)
	// Cache returns the last saved list (false when there is none).
	Cache func() ([]Country, bool)
	// Save/Load persist the per-network memory; they default to the
	// settings store and are overridden in tests.
	Save func(m bootstrapMemory)
	Load func() (bootstrapMemory, bool)
}

// BootstrapReport is one path attempt, for the logs and diagnostics view.
type BootstrapReport struct {
	Path   string `json:"path"`
	OK     bool   `json:"ok"`
	Err    string `json:"error,omitempty"`
	Millis int64  `json:"ms"`
}

type bootstrapMemory struct {
	Network  string               `json:"network"`
	LastGood string               `json:"last_good,omitempty"`
	BadUntil map[string]time.Time `json:"bad_until,omitempty"`
}

type namedPath struct {
	name  string
	fetch func() ([]Country, error)
}

type Bootstrap struct {
	mu    sync.Mutex
	mem   bootstrapMemory
	opts  BootstrapOptions
	paths []namedPath
}

// NewBootstrap builds the manager and loads this network's memory.
func NewBootstrap(opts BootstrapOptions) *Bootstrap {
	b := &Bootstrap{opts: opts}
	if b.opts.Sig == nil {
		b.opts.Sig = netx.NetworkSignature
	}
	if b.opts.ProxyURL != "" {
		if dial, derr := netx.UpstreamDialer(b.opts.ProxyURL); derr == nil && dial != nil {
			b.paths = append(b.paths, namedPath{"proxy", func() ([]Country, error) {
				tr := &http.Transport{Proxy: nil, DialContext: dial, ForceAttemptHTTP2: true, TLSHandshakeTimeout: 15 * time.Second}
				defer tr.CloseIdleConnections()
				return FetchServerListWith(&http.Client{Transport: tr, Timeout: 30 * time.Second})
			}})
		}
	}
	b.paths = append(b.paths,
		namedPath{"direct", FetchServerList},
	)
	if opts.Masque != nil {
		b.paths = append(b.paths, namedPath{"masque", opts.Masque})
	}
	if opts.Cache != nil {
		b.paths = append(b.paths, namedPath{"cache", func() ([]Country, error) {
			if list, ok := opts.Cache(); ok && len(list) > 0 {
				return list, nil
			}
			return nil, errors.New("no saved server list")
		}})
	}
	// The compiled snapshot: the last network-independent resort, so the
	// server page is populated and a connect is attempted even when every
	// live path is refused.
	b.paths = append(b.paths, namedPath{"builtin", BuiltinServerList})
	b.mu.Lock()
	if b.opts.Load != nil {
		if mem, ok := b.opts.Load(); ok {
			b.mem = mem
		}
	} else {
		var mem bootstrapMemory
		if store.LoadJSON(bootstrapStateFile, &mem) {
			b.mem = mem
		}
	}
	b.mu.Unlock()
	return b
}

// race runs the ordered paths staggered (each 2s after the previous one);
// the first success wins and cancels the rest. A sequential walk made the
// server page wait out a 30s direct timeout before ever trying the proxy.
func (b *Bootstrap) race(order []namedPath) ([]Country, []BootstrapReport, error) {
	type res struct {
		list []Country
		rep  BootstrapReport
		err  error
	}
	ch := make(chan res, len(order))
	done := make(chan struct{})
	defer close(done)
	for i, p := range order {
		go func(i int, p namedPath) {
			select {
			case <-time.After(time.Duration(i) * raceStagger):
			case <-done:
				return
			}
			start := time.Now()
			l, e := fetchWithTimeout(p, bootstrapPathTimeout)
			r := BootstrapReport{Path: p.name, OK: e == nil, Millis: time.Since(start).Milliseconds()}
			if e != nil {
				r.Err = e.Error()
			}
			ch <- res{l, r, e}
		}(i, p)
	}
	var reports []BootstrapReport
	var lastErr error
	for range order {
		r := <-ch
		reports = append(reports, r.rep)
		if r.err == nil {
			// remember the winner; the losers of this race are transient
			// (or genuinely blocked, and their own failure is not recorded
			// as a cooldown here — only the sequential walk does that).
			return r.list, reports, nil
		}
		lastErr = r.err
	}
	return nil, reports, lastErr
}

// ServerList downloads the list over the best available path and reports
// every attempt. The paths run staggered in parallel; the remembered-good
// path for this network goes first and a path that failed here recently
// sits out its cooldown.
func (b *Bootstrap) ServerList() (list []Country, reports []BootstrapReport, err error) {
	b.mu.Lock()
	sig := b.opts.Sig()
	if b.mem.Network != sig {
		// A different network: its memory does not apply, start fresh.
		b.mem = bootstrapMemory{Network: sig}
	}
	order := make([]namedPath, 0, len(b.paths))
	for _, p := range b.paths {
		if p.name == b.mem.LastGood {
			order = append([]namedPath{p}, order...)
		} else if b.mem.BadUntil[p.name].After(time.Now()) {
			continue // failed here recently
		} else {
			order = append(order, p)
		}
	}
	b.mu.Unlock()
	if len(order) == 0 {
		return nil, nil, errors.New("no bootstrap path is available")
	}

	list, reports, err = b.race(order)
	for _, r := range reports {
		netdiag.Report("bootstrap:"+r.Path, netdiag.HTTP,
			map[bool]string{true: netdiag.OK, false: netdiag.HTTPBlocked}[r.OK],
			r.Err, time.Duration(r.Millis)*time.Millisecond)
		// Every reported path gets its memory: the winner becomes
		// LastGood, and a live path that failed here sits out its
		// cooldown (cache/builtin are exempt — they always run).
		if r.OK {
			b.remember(sig, r.Path, nil)
			return list, reports, nil
		}
		b.remember(sig, r.Path, errors.New(r.Err))
	}
	var firstErr error
	for _, r := range reports {
		if r.Err != "" {
			firstErr = errors.New(r.Err)
			break
		}
	}
	if firstErr == nil {
		firstErr = errors.New("no bootstrap path is available")
	}
	return nil, reports, firstErr
}

// remember updates the per-network memory: the winner is remembered, a
// failing path sits out its cooldown (the cache fallback never gets bad).
func (b *Bootstrap) remember(sig, path string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mem.Network != sig {
		b.mem = bootstrapMemory{Network: sig}
	}
	if err == nil {
		b.mem.LastGood = path
		delete(b.mem.BadUntil, path)
	} else if path != "cache" && path != "builtin" && b.mem.LastGood != path {
		// A failed live path sits out its cooldown; cache and builtin are
		// exempt — they always run.
		if b.mem.BadUntil == nil {
			b.mem.BadUntil = map[string]time.Time{}
		}
		b.mem.BadUntil[path] = time.Now().Add(badPathCooldown)
	}
	if b.opts.Save != nil {
		b.opts.Save(b.mem)
		return
	}
	store.SaveJSON(bootstrapStateFile, b.mem)
}

// Reset forgets the per-network memory (Reset Network Configuration).
func (b *Bootstrap) Reset() {
	b.mu.Lock()
	sig := b.opts.Sig()
	b.mem = bootstrapMemory{Network: sig}
	b.mu.Unlock()
	if b.opts.Save != nil {
		b.opts.Save(b.mem)
		return
	}
	store.SaveJSON(bootstrapStateFile, b.mem)
}
