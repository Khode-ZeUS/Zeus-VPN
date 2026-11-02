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

// ServerList downloads the list over the best available path and reports
// every attempt. The remembered-good path for this network goes first; a
// path that failed here recently sits out its cooldown.
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

	var lastErr error
	for _, p := range order {
		start := time.Now()
		list, err = p.fetch()
		r := BootstrapReport{Path: p.name, Millis: time.Since(start).Milliseconds()}
		if err != nil {
			r.Err = err.Error()
			lastErr = err
		} else {
			r.OK = true
		}
		reports = append(reports, r)
		netdiag.Report("bootstrap:"+p.name, netdiag.HTTP, map[bool]string{true: netdiag.OK, false: netdiag.HTTPBlocked}[r.OK], r.Err, time.Since(start))
		if err == nil {
			b.remember(sig, p.name, nil)
			return list, reports, nil
		}
		b.remember(sig, p.name, err)
	}
	if lastErr == nil {
		lastErr = errors.New("no bootstrap path is available")
	}
	return nil, reports, lastErr
}

// remember updates the per-network memory: the winner is remembered, a
// failing path sits out its cooldown (the cache fallback never gets bad).
func (b *Bootstrap) remember(sig, path string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.mem.Network != sig {
		b.mem = bootstrapMemory{Network: sig}
	}
	if path == "cache" {
		return
	}
	if err == nil {
		b.mem.LastGood = path
		delete(b.mem.BadUntil, path)
	} else if b.mem.LastGood != path {
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
