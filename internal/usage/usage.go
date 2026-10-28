// Package usage records where the tunnelled data went: per destination
// (the server host the connection targeted — persisted, works on every
// platform and mode because it is counted where the tunnel streams carry
// the bytes) and per application (Windows only, resolved from the OS TCP
// table; kept in memory).
package usage

import (
	"context"
	"net"
	"sort"
	"strings"
	"sync"
	"time"

	"zeusvpn/internal/store"
)

const (
	hostsFile = "usage.json"
	maxKeep   = 2000 // hosts kept; the smallest are dropped first
)

// ---------- per destination (host) ----------

type Entry struct {
	Host string `json:"host"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
	Last int64  `json:"last"` // unix seconds
}

type hostsSnapshot struct {
	Entries []Entry `json:"entries"`
}

var (
	hosts     = map[string]*Entry{}
	hostsMu   sync.Mutex
	hostsNice bool
)

// Normalize turns "host:port" into the label usage is tracked under.
func Normalize(target string) string {
	host := target
	if i := strings.LastIndex(host, ":"); i > strings.LastIndex(host, "]") {
		host = host[:i]
	}
	host = strings.Trim(strings.Trim(host, "[]"), ".")
	return strings.ToLower(host)
}

// TrackHost adds n bytes (upload or download) to a destination.
func TrackHost(host string, up, down int64) {
	if host == "" || (up <= 0 && down <= 0) {
		return
	}
	hostsMu.Lock()
	defer hostsMu.Unlock()
	e, ok := hosts[host]
	if !ok {
		if len(hosts) >= maxKeep {
			dropSmallestHostLocked()
		}
		e = &Entry{Host: host}
		hosts[host] = e
	}
	e.Up += up
	e.Down += down
	e.Last = time.Now().Unix()
	hostsNice = true
}

func dropSmallestHostLocked() {
	minKey, min := "", int64(1<<62)
	for k, e := range hosts {
		if t := e.Up + e.Down; t < min {
			min, minKey = t, k
		}
	}
	if minKey != "" {
		delete(hosts, minKey)
	}
}

// TopHosts returns the heaviest destinations, largest total first.
func TopHosts(limit int) []Entry {
	hostsMu.Lock()
	defer hostsMu.Unlock()
	out := make([]Entry, 0, len(hosts))
	for _, e := range hosts {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Up+out[i].Down, out[j].Up+out[j].Down
		if a != b {
			return a > b
		}
		return out[i].Host < out[j].Host
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// LoadHosts restores the saved history (once at startup).
func LoadHosts() {
	var s hostsSnapshot
	if !store.LoadJSON(hostsFile, &s) {
		return
	}
	hostsMu.Lock()
	defer hostsMu.Unlock()
	for i := range s.Entries {
		e := s.Entries[i]
		if e.Host != "" {
			hosts[e.Host] = &e
		}
	}
}

// SaveHosts writes the history if it changed since the last save.
func SaveHosts() {
	hostsMu.Lock()
	if !hostsNice {
		hostsMu.Unlock()
		return
	}
	s := hostsSnapshot{Entries: make([]Entry, 0, len(hosts))}
	for _, e := range hosts {
		s.Entries = append(s.Entries, *e)
	}
	hostsNice = false
	hostsMu.Unlock()
	store.SaveJSON(hostsFile, &s)
}

// PruneHosts drops destinations not seen for a month and saves.
func PruneHosts() {
	cut := time.Now().AddDate(0, -1, 0).Unix()
	hostsMu.Lock()
	for k, e := range hosts {
		if e.Last < cut {
			delete(hosts, k)
			hostsNice = true
		}
	}
	hostsMu.Unlock()
	SaveHosts()
}

// ---------- per application (Windows) ----------

type AppHost struct {
	Host string `json:"host"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

// App is one application's totals plus the destinations it used. The hosts
// map is runtime state; ListApps serialises it as a sorted Hosts slice.
type App struct {
	Name  string    `json:"name"`
	Up    int64     `json:"up"`
	Down  int64     `json:"down"`
	Hosts []AppHost `json:"hosts"`

	hosts map[string]*Entry
}

var (
	apps   = map[string]*App{}
	appsMu sync.Mutex
)

func app(name string) *App {
	if name == "" {
		return nil
	}
	appsMu.Lock()
	defer appsMu.Unlock()
	a, ok := apps[name]
	if !ok {
		a = &App{Name: name, hosts: map[string]*Entry{}}
		apps[name] = a
	}
	return a
}

func add(a *App, up, down int64) {
	if a == nil || (up == 0 && down == 0) {
		return
	}
	appsMu.Lock()
	a.Up += up
	a.Down += down
	appsMu.Unlock()
}

// TrackAppHost records bytes an app sent to one destination (totals stay
// with the app's connection counting; only the breakdown is kept here).
func TrackAppHost(a *App, host string, up, down int64) {
	if a == nil || host == "" || (up <= 0 && down <= 0) {
		return
	}
	appsMu.Lock()
	defer appsMu.Unlock()
	if a.hosts == nil {
		a.hosts = map[string]*Entry{}
	}
	e, ok := a.hosts[host]
	if !ok {
		e = &Entry{Host: host}
		a.hosts[host] = e
	}
	e.Up += up
	e.Down += down
	e.Last = time.Now().Unix()
}

// ListApps returns every app that used the tunnel, heaviest total first,
// each with its destinations sorted the same way.
func ListApps() []App {
	appsMu.Lock()
	defer appsMu.Unlock()
	out := make([]App, 0, len(apps))
	for _, a := range apps {
		c := App{Name: a.Name, Up: a.Up, Down: a.Down, hosts: a.hosts}
		for _, e := range a.hosts {
			c.Hosts = append(c.Hosts, AppHost{Host: e.Host, Up: e.Up, Down: e.Down})
		}
		sort.Slice(c.Hosts, func(i, j int) bool {
			x, y := c.Hosts[i].Up+c.Hosts[i].Down, c.Hosts[j].Up+c.Hosts[j].Down
			if x != y {
				return x > y
			}
			return c.Hosts[i].Host < c.Hosts[j].Host
		})
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Up+out[i].Down > out[j].Up+out[j].Down })
	return out
}

// ---------- app identity plumbing ----------

type appKey struct{}

// WithApp marks a dial context as belonging to an app, so the tunnel
// streams it opens attribute their per-destination bytes to that app.
func WithApp(ctx context.Context, a *App) context.Context {
	if a == nil {
		return ctx
	}
	return context.WithValue(ctx, appKey{}, a)
}

// AppFromContext returns the app a dial context belongs to (nil if none).
func AppFromContext(ctx context.Context) *App {
	if ctx == nil {
		return nil
	}
	a, _ := ctx.Value(appKey{}).(*App)
	return a
}

// ConnApp returns the app a wrapped client connection belongs to.
func ConnApp(c net.Conn) *App { return AppOf(c) }

// AppOf unwraps connection wrappers (peekConn and friends) to find the
// counted app.
func AppOf(c net.Conn) *App {
	for depth := 0; c != nil && depth < 5; depth++ {
		if cc, ok := c.(*countingConn); ok {
			return cc.a
		}
		if u, ok := c.(interface{ Unwrap() net.Conn }); ok {
			c = u.Unwrap()
			continue
		}
		return nil
	}
	return nil
}

// ---------- shared ----------

// Reset forgets everything (destinations and apps).
func Reset() {
	hostsMu.Lock()
	hosts = map[string]*Entry{}
	hostsNice = true
	hostsMu.Unlock()
	appsMu.Lock()
	apps = map[string]*App{}
	appsMu.Unlock()
	SaveHosts()
}

// ---------- connection wrappers ----------

// Wrap resolves the app that owns a local client connection and returns a
// connection that counts its bytes on that app. The connection is returned
// unchanged on platforms without per-app support.
func Wrap(conn net.Conn) net.Conn {
	return WrapAddr(conn, conn.RemoteAddr().String())
}

// WrapAddr is Wrap with the owning connection's local address given
// explicitly: a TUN stack's conn reports the destination as RemoteAddr,
// while the app's real address arrives separately as the source.
// Connections whose owner cannot be determined are counted under
// "(unknown)" so the panel never looks broken when only the name lookup
// fails.
func WrapAddr(conn net.Conn, local string) net.Conn {
	a := app(owner(local))
	if a == nil {
		a = app("(unknown)")
	}
	return &countingConn{Conn: conn, a: a}
}

type countingConn struct {
	net.Conn
	a *App
}

// Unwrap lets AppOf see through further wrappers.
func (c *countingConn) Unwrap() net.Conn { return c.Conn }

// Read is data the app uploaded.
func (c *countingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	add(c.a, int64(n), 0)
	return n, err
}

// Write is data delivered to the app.
func (c *countingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	add(c.a, 0, int64(n))
	return n, err
}
