package tunnel

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/net/http2"
)

// RoundTrip forwards a plain-HTTP request as a proxy request (absolute URI)
// over the HTTP/2 session. Mozilla's proxies refuse CONNECT to port 80, so
// this is how unencrypted HTTP has to travel, just like in Firefox.
func (u *Upstream) RoundTrip(req *http.Request) (*http.Response, error) {
	replayable := req.Body == nil || req.Body == http.NoBody
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		cc, err := u.session(req.Context())
		if err != nil {
			return nil, err
		}
		r := req.Clone(req.Context())
		r.Header.Set("Proxy-Authorization", "Bearer "+*u.token.Load())
		resp, err := cc.RoundTrip(r)
		if err != nil {
			lastErr = err
			if st := cc.State(); replayable && (st.Closed || st.Closing || errors.Is(err, http2.ErrNoCachedConn)) {
				u.discard(cc)
				continue
			}
			return nil, err
		}
		resp.Body = &countingBody{ReadCloser: resp.Body, u: u}
		return resp, nil
	}
	return nil, lastErr
}

type countingBody struct {
	io.ReadCloser
	u *Upstream
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.u.Down.Add(int64(n))
	return n, err
}

// RoundTripFunc sends one plain-HTTP request to its destination.
type RoundTripFunc func(*http.Request) (*http.Response, error)

// ServeHTTPForward reads HTTP/1.x requests from a client connection and
// answers each through rt. defaultHost fills in requests without a Host
// header (transparent TUN connections).
func ServeHTTPForward(c net.Conn, br *bufio.Reader, first *http.Request, defaultHost string, rt RoundTripFunc) {
	req := first
	for {
		if req == nil {
			c.SetReadDeadline(time.Now().Add(60 * time.Second))
			var err error
			if req, err = http.ReadRequest(br); err != nil {
				return
			}
			c.SetReadDeadline(time.Time{})
		}
		if req.URL.Host == "" {
			req.URL.Host = req.Host
		}
		if req.URL.Host == "" {
			req.URL.Host = defaultHost
		}
		if req.URL.Host == "" {
			fmt.Fprintf(c, "HTTP/1.1 400 Bad Request\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		if req.URL.Scheme == "" {
			req.URL.Scheme = "http"
		}
		keepAlive := !req.Close && req.ProtoAtLeast(1, 1) && !strings.EqualFold(req.Header.Get("Proxy-Connection"), "close")
		for _, h := range hopHeaders {
			req.Header.Del(h)
		}
		req.RequestURI = ""
		resp, err := rt(req)
		if err != nil {
			fmt.Fprintf(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		if !keepAlive {
			resp.Close = true
		}
		err = resp.Write(c)
		resp.Body.Close()
		if err != nil || !keepAlive {
			return
		}
		req = nil
	}
}
