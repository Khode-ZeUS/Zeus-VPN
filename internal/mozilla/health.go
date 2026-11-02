package mozilla

import (
	"math/rand/v2"
	"sort"
	"sync"
	"time"
)

// Endpoint health: how the VPN servers have behaved in this process.
// Selection prefers servers that worked recently with low latency and
// temporarily drops the unreachable ones, instead of the old uniform
// random pick that could land on a filtered address every time.

const (
	// endpointCooldown is how long a server that just failed sits out.
	endpointCooldown = 10 * time.Minute
	// unknownLatency scores servers with no measurement yet.
	unknownLatency = int64(4000)
	// pickTopRandom chooses among the best few, so users on the same city
	// do not all crowd one server.
	pickTopRandom = 3
)

type endpointHealth struct {
	fails     int
	lastFail  time.Time
	latencyMs int64
}

var epHealth sync.Map // Endpoint.Addr() -> *endpointHealth

func healthFor(addr string) *endpointHealth {
	if v, ok := epHealth.Load(addr); ok {
		return v.(*endpointHealth)
	}
	s := &endpointHealth{latencyMs: unknownLatency}
	epHealth.Store(addr, s)
	return s
}

// MarkEndpointOK records a successful connection (or a measured latency)
// for the endpoint; it also clears its failure history.
func MarkEndpointOK(addr string, latencyMs int64) {
	s := healthFor(addr)
	if latencyMs > 0 {
		s.latencyMs = latencyMs
	}
	s.fails = 0
}

// MarkEndpointFail records a failed connection attempt for the endpoint.
func MarkEndpointFail(addr string) {
	s := healthFor(addr)
	s.fails++
	s.lastFail = time.Now()
}

// PickHealthyEndpoint picks a server from the candidates: recent failures
// go last, working servers are ordered by their last known latency, and
// the choice among the best few is random. With no health knowledge (a
// fresh install) it degrades to the old uniform-random pick.
func PickHealthyEndpoint(cands []Endpoint) Endpoint {
	if len(cands) == 0 {
		return Endpoint{}
	}
	now := time.Now()
	type scored struct {
		ep    Endpoint
		score int64
	}
	var good, bad []scored
	for _, ep := range cands {
		h := healthFor(ep.Addr())
		if h.lastFail.After(now.Add(-endpointCooldown)) {
			bad = append(bad, scored{ep, int64(h.fails)})
			continue
		}
		ms := h.latencyMs
		if ms <= 0 {
			ms = unknownLatency
		}
		good = append(good, scored{ep, ms + rand.Int64N(150)})
	}
	pool := good
	if len(pool) == 0 {
		pool = bad
	}
	sort.Slice(pool, func(i, j int) bool { return pool[i].score < pool[j].score })
	top := pickTopRandom
	if len(pool) < top {
		top = len(pool)
	}
	return pool[rand.IntN(top)].ep
}
