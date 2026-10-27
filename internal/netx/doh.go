package netx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Public DoH servers addressed by IP, so resolving needs no DNS itself.
var dohEndpoints = []string{"https://1.1.1.1/dns-query", "https://9.9.9.9/dns-query", "https://8.8.8.8/dns-query"}

var dohClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		Proxy:               nil,
		DialContext:         DialContext,
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 4 * time.Second,
		IdleConnTimeout:     60 * time.Second,
	},
}

type cached struct {
	addrs []netip.Addr
	at    time.Time
}

var cache sync.Map // host -> cached

// LookupHost resolves host over DNS-over-HTTPS (immune to local DNS
// poisoning), falling back to the system resolver.
func LookupHost(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	if c, ok := cache.Load(host); ok && time.Since(c.(cached).at) < 10*time.Minute {
		return c.(cached).addrs, nil
	}
	addrs := lookupDoH(ctx, host)
	if len(addrs) == 0 {
		var err error
		if addrs, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host); err != nil {
			return nil, err
		}
	}
	// IPv4 first: it is far more reliable on residential networks.
	sorted := make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if a.Unmap().Is4() {
			sorted = append(sorted, a.Unmap())
		}
	}
	for _, a := range addrs {
		if !a.Unmap().Is4() {
			sorted = append(sorted, a)
		}
	}
	cache.Store(host, cached{sorted, time.Now()})
	return sorted, nil
}

// DialResolved dials "host:port" using LookupHost. Addresses are raced,
// each started 300 ms after the previous one, and the first connection
// wins: some addresses of a CDN can be blackholed while others work.
func DialResolved(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, errors.New("no addresses for " + host)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		c   net.Conn
		err error
	}
	results := make(chan result, len(addrs))
	for i, a := range addrs {
		go func(i int, a netip.Addr) {
			select {
			case <-time.After(time.Duration(i) * 300 * time.Millisecond):
			case <-ctx.Done():
				results <- result{nil, ctx.Err()}
				return
			}
			c, err := DialContext(ctx, network, net.JoinHostPort(a.String(), port))
			results <- result{c, err}
		}(i, a)
	}
	var firstErr error
	for range addrs {
		r := <-results
		if r.err == nil {
			cancel()
			// Close any other connection that still completes.
			go func(n int) {
				for i := 0; i < n; i++ {
					if late := <-results; late.c != nil {
						late.c.Close()
					}
				}
			}(len(addrs) - 1)
			return r.c, nil
		}
		if firstErr == nil || errors.Is(firstErr, context.Canceled) {
			firstErr = r.err
		}
	}
	return nil, firstErr
}

// lookupDoH asks every resolver and merges the answers: one resolver may
// return a single address that happens to be filtered or MITM'd, while the
// others still know working ones. The first caller-visible answer usually
// arrives within milliseconds of the fastest single reply.
func lookupDoH(ctx context.Context, host string) []netip.Addr {
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	type result struct{ addrs []netip.Addr }
	ch := make(chan result, len(dohEndpoints))
	for _, ep := range dohEndpoints {
		go func(ep string) {
			addrs, _ := queryDoH(ctx, ep, host, dnsmessage.TypeA)
			if len(addrs) == 0 {
				addrs, _ = queryDoH(ctx, ep, host, dnsmessage.TypeAAAA)
			}
			ch <- result{addrs}
		}(ep)
	}
	var merged []netip.Addr
	seen := map[string]bool{}
	for range dohEndpoints {
		select {
		case r := <-ch:
			for _, a := range r.addrs {
				if !seen[a.String()] {
					seen[a.String()] = true
					merged = append(merged, a)
				}
			}
		case <-ctx.Done():
			if len(merged) > 0 {
				return merged
			}
			return nil
		}
	}
	return merged
}

func queryDoH(ctx context.Context, endpoint, host string, qtype dnsmessage.Type) ([]netip.Addr, error) {
	name, err := dnsmessage.NewName(host + ".")
	if err != nil {
		return nil, err
	}
	msg := dnsmessage.Message{
		Header:    dnsmessage.Header{RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: qtype, Class: dnsmessage.ClassINET}},
	}
	q, err := msg.Pack()
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(q))
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := dohClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, errors.New(resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	var p dnsmessage.Parser
	if _, err := p.Start(body); err != nil {
		return nil, err
	}
	p.SkipAllQuestions()
	var out []netip.Addr
	for {
		h, err := p.AnswerHeader()
		if err != nil {
			break
		}
		switch h.Type {
		case dnsmessage.TypeA:
			r, err := p.AResource()
			if err == nil {
				out = append(out, netip.AddrFrom4(r.A))
			}
		case dnsmessage.TypeAAAA:
			r, err := p.AAAAResource()
			if err == nil {
				out = append(out, netip.AddrFrom16(r.AAAA))
			}
		default:
			p.SkipAnswer()
		}
	}
	return out, nil
}
