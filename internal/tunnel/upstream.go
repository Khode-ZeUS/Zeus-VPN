// Package tunnel carries TCP connections to Mozilla's VPN proxies as HTTP/2
// CONNECT streams (RFC 9113 section 8.5), authenticated with the Guardian
// proxy-pass bearer token.
package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"zeusvpn/internal/netx"

	"golang.org/x/net/http2"
)

// RefusedError is returned when the proxy answers CONNECT with a non-2xx status.
type RefusedError struct{ Status int }

func (e *RefusedError) Error() string {
	return fmt.Sprintf("upstream refused CONNECT with status %d", e.Status)
}

type Upstream struct {
	host  string
	port  int
	addrs []netip.Addr
	token atomic.Pointer[string]

	transport *http2.Transport
	mu        sync.Mutex
	conns     []*http2.ClientConn
	closed    bool

	// Counters for every byte carried through the tunnel.
	Up, Down atomic.Int64
}

// NewUpstream resolves the proxy host once up front: in TUN mode the system
// resolver may itself depend on the tunnel, so reconnects dial the cached IPs.
func NewUpstream(ctx context.Context, host string, port int, token string) (*Upstream, error) {
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("could not resolve %s: %w", host, err)
	}
	// Prefer IPv4: it is far more reliable on residential networks.
	var v4, v6 []netip.Addr
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			v4 = append(v4, ip.Unmap())
		} else {
			v6 = append(v6, ip)
		}
	}
	u := &Upstream{
		host:  host,
		port:  port,
		addrs: append(v4, v6...),
		transport: &http2.Transport{
			ReadIdleTimeout:    30 * time.Second,
			PingTimeout:        15 * time.Second,
			WriteByteTimeout:   30 * time.Second,
			DisableCompression: true,
		},
	}
	u.token.Store(&token)
	return u, nil
}

func (u *Upstream) Host() string { return u.host }

func (u *Upstream) SetToken(token string) { u.token.Store(&token) }

// Warmup establishes the first session so connection problems surface
// before we claim to be connected.
func (u *Upstream) Warmup(ctx context.Context) error {
	cc, err := u.newConn(ctx)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.conns = append(u.conns, cc)
	u.mu.Unlock()
	return nil
}

func (u *Upstream) newConn(ctx context.Context) (*http2.ClientConn, error) {
	var lastErr error
	for _, ip := range u.addrs {
		dctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		raw, err := netx.DialContext(dctx, "tcp", net.JoinHostPort(ip.String(), strconv.Itoa(u.port)))
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		tc := tls.Client(raw, &tls.Config{ServerName: u.host, NextProtos: []string{"h2"}, MinVersion: tls.VersionTLS12})
		err = tc.HandshakeContext(dctx)
		cancel()
		if err != nil {
			raw.Close()
			lastErr = err
			continue
		}
		if p := tc.ConnectionState().NegotiatedProtocol; p != "h2" {
			tc.Close()
			lastErr = fmt.Errorf("%s did not negotiate HTTP/2 (got %q)", u.host, p)
			continue
		}
		cc, err := u.transport.NewClientConn(tc)
		if err != nil {
			tc.Close()
			lastErr = err
			continue
		}
		return cc, nil
	}
	if lastErr == nil {
		lastErr = errors.New("no addresses")
	}
	return nil, fmt.Errorf("could not connect to VPN server %s: %w", u.host, lastErr)
}

// session returns a client conn with a reserved stream slot.
func (u *Upstream) session(ctx context.Context) (*http2.ClientConn, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return nil, net.ErrClosed
	}
	live := u.conns[:0]
	var picked *http2.ClientConn
	for _, cc := range u.conns {
		st := cc.State()
		if st.Closed || st.Closing {
			continue
		}
		live = append(live, cc)
		if picked == nil && cc.ReserveNewRequest() {
			picked = cc
		}
	}
	u.conns = live
	if picked != nil {
		return picked, nil
	}
	cc, err := u.newConn(ctx)
	if err != nil {
		return nil, err
	}
	u.conns = append(u.conns, cc)
	if !cc.ReserveNewRequest() {
		return nil, errors.New("new upstream session refused streams")
	}
	return cc, nil
}

func (u *Upstream) discard(cc *http2.ClientConn) {
	u.mu.Lock()
	for i, c := range u.conns {
		if c == cc {
			u.conns = append(u.conns[:i], u.conns[i+1:]...)
			break
		}
	}
	u.mu.Unlock()
	cc.Close()
}

// Dial opens a tunneled TCP connection to target ("host:port"). Passing a
// hostname rather than an IP lets the proxy resolve it, avoiding local DNS.
func (u *Upstream) Dial(ctx context.Context, target string) (net.Conn, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cc, err := u.session(ctx)
		if err != nil {
			return nil, err
		}
		pr, pw := io.Pipe()
		req := &http.Request{
			Method: "CONNECT",
			URL:    &url.URL{Host: target},
			Host:   target,
			Header: http.Header{"Proxy-Authorization": {"Bearer " + *u.token.Load()}},
			Body:   pr,
			ContentLength: -1,
		}
		req = req.WithContext(context.WithoutCancel(ctx))
		type result struct {
			resp *http.Response
			err  error
		}
		ch := make(chan result, 1)
		go func() {
			resp, err := cc.RoundTrip(req)
			ch <- result{resp, err}
		}()
		var res result
		select {
		case res = <-ch:
		case <-ctx.Done():
			pw.CloseWithError(ctx.Err())
			go func() {
				if r := <-ch; r.resp != nil {
					r.resp.Body.Close()
				}
			}()
			return nil, ctx.Err()
		}
		if res.err != nil {
			pw.Close()
			lastErr = res.err
			// A dead session: drop it and retry once on a fresh one.
			if st := cc.State(); st.Closed || st.Closing || errors.Is(res.err, http2.ErrNoCachedConn) {
				u.discard(cc)
				continue
			}
			return nil, res.err
		}
		if res.resp.StatusCode < 200 || res.resp.StatusCode > 299 {
			pw.Close()
			res.resp.Body.Close()
			return nil, &RefusedError{Status: res.resp.StatusCode}
		}
		return &streamConn{u: u, body: res.resp.Body, w: pw, target: target}, nil
	}
	return nil, lastErr
}

// Close tears down all sessions; open streams fail.
func (u *Upstream) Close() {
	u.mu.Lock()
	u.closed = true
	conns := u.conns
	u.conns = nil
	u.mu.Unlock()
	for _, cc := range conns {
		cc.Close()
	}
}

type streamConn struct {
	u      *Upstream
	body   io.ReadCloser
	w      *io.PipeWriter
	target string
	once   sync.Once
}

func (c *streamConn) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	c.u.Down.Add(int64(n))
	return n, err
}

func (c *streamConn) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.u.Up.Add(int64(n))
	return n, err
}

func (c *streamConn) CloseWrite() error { return c.w.Close() }

func (c *streamConn) Close() error {
	c.once.Do(func() {
		c.w.Close()
		c.body.Close()
	})
	return nil
}

type tunnelAddr string

func (a tunnelAddr) Network() string { return "tcp" }
func (a tunnelAddr) String() string  { return string(a) }

func (c *streamConn) LocalAddr() net.Addr                { return tunnelAddr("zeusvpn") }
func (c *streamConn) RemoteAddr() net.Addr               { return tunnelAddr(c.target) }
func (c *streamConn) SetDeadline(t time.Time) error      { return nil }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return nil }
