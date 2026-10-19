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

// DialResolved dials "host:port" using LookupHost, trying each address.
func DialResolved(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addrs, err := LookupHost(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, a := range addrs {
		c, err := DialContext(ctx, network, net.JoinHostPort(a.String(), port))
		if err == nil {
			return c, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no addresses for " + host)
	}
	return nil, lastErr
}

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
	for range dohEndpoints {
		select {
		case r := <-ch:
			if len(r.addrs) > 0 {
				return r.addrs
			}
		case <-ctx.Done():
			return nil
		}
	}
	return nil
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
