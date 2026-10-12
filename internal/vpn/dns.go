package vpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

var dohServers = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/dns-query"}

// DoH answers DNS queries with DNS-over-HTTPS carried through the tunnel, so
// lookups are neither visible to nor poisoned by the local network.
type DoH struct {
	client *http.Client

	mu    sync.Mutex
	cache map[string]cacheEntry
}

type cacheEntry struct {
	resp    []byte
	expires time.Time
}

func NewDoH(dial func(ctx context.Context, target string) (net.Conn, error)) *DoH {
	tr := &http.Transport{
		Proxy:               nil,
		DialContext:         func(ctx context.Context, _, addr string) (net.Conn, error) { return dial(ctx, addr) },
		ForceAttemptHTTP2:   true,
		TLSHandshakeTimeout: 8 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 2,
	}
	return &DoH{client: &http.Client{Transport: tr, Timeout: 8 * time.Second}, cache: map[string]cacheEntry{}}
}

func (d *DoH) Close() { d.client.CloseIdleConnections() }

func cacheKey(q []byte) (string, uint16, bool) {
	var p dnsmessage.Parser
	h, err := p.Start(q)
	if err != nil {
		return "", 0, false
	}
	question, err := p.Question()
	if err != nil {
		return "", 0, false
	}
	return strings.ToLower(question.Name.String()) + "|" + question.Type.String() + "|" + question.Class.String(), h.ID, true
}

func minTTL(resp []byte) time.Duration {
	var p dnsmessage.Parser
	h, err := p.Start(resp)
	if err != nil {
		return 0
	}
	if h.RCode == dnsmessage.RCodeNameError {
		return 30 * time.Second
	}
	if h.RCode != dnsmessage.RCodeSuccess {
		return 0
	}
	p.SkipAllQuestions()
	ttl := uint32(600)
	found := false
	for {
		rh, err := p.AnswerHeader()
		if err != nil {
			break
		}
		found = true
		if rh.TTL < ttl {
			ttl = rh.TTL
		}
		p.SkipAnswer()
	}
	if !found {
		return 30 * time.Second
	}
	if ttl < 10 {
		ttl = 10
	}
	return time.Duration(ttl) * time.Second
}

func (d *DoH) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	key, id, ok := cacheKey(query)
	if ok {
		d.mu.Lock()
		e, hit := d.cache[key]
		d.mu.Unlock()
		if hit && time.Now().Before(e.expires) {
			out := append([]byte(nil), e.resp...)
			binary.BigEndian.PutUint16(out, id)
			return out, nil
		}
	}
	// DoH recommends ID 0 for cacheability.
	q := append([]byte(nil), query...)
	binary.BigEndian.PutUint16(q, 0)
	var lastErr error
	for _, server := range dohServers {
		resp, err := d.post(ctx, server, q)
		if err != nil {
			lastErr = err
			continue
		}
		if ok {
			if ttl := minTTL(resp); ttl > 0 {
				d.mu.Lock()
				if len(d.cache) > 4096 {
					d.cache = map[string]cacheEntry{}
				}
				d.cache[key] = cacheEntry{resp: resp, expires: time.Now().Add(ttl)}
				d.mu.Unlock()
			}
		}
		out := append([]byte(nil), resp...)
		binary.BigEndian.PutUint16(out, id)
		return out, nil
	}
	return nil, lastErr
}

func (d *DoH) post(ctx context.Context, server string, q []byte) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", server, bytes.NewReader(q))
	req.Header.Set("Content-Type", "application/dns-message")
	req.Header.Set("Accept", "application/dns-message")
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("DoH %s: HTTP %d", server, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return nil, err
	}
	if len(b) < 12 {
		return nil, errors.New("short DNS response")
	}
	return b, nil
}
