package netx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const workerFrameSize = 64 << 10

var workerWebSocketDialer = websocket.Dialer{HandshakeTimeout: 8 * time.Second}

func workerDialer(endpoint url.URL, dialer websocket.Dialer, token string, isManaged bool) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if network != "tcp" {
			return nil, errors.New("the Worker relay carries TCP only")
		}
		if token == "" && !isManaged {
			return nil, errors.New("Worker key is missing; start Zeus VPN from the PowerShell session that set ZEUS_WORKER_PILOT_TOKEN")
		}
		u := endpoint
		u.RawQuery = url.Values{"target": {addr}}.Encode()
		header := make(http.Header)
		header.Set("User-Agent", "ZeusVPN-WorkerRelay/1.0")
		if !isManaged {
			header.Set("Authorization", "Bearer "+token)
		}
		ws, resp, err := dialer.DialContext(ctx, u.String(), header)
		if err != nil {
			if resp != nil {
				resp.Body.Close()
				if isManaged && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusServiceUnavailable) {
					return nil, errors.New("the built-in relay is unavailable; try again later")
				}
				if resp.StatusCode == http.StatusUnauthorized {
					return nil, errors.New("the Worker rejected the key; check the Worker secret and ZEUS_WORKER_PILOT_TOKEN")
				}
				return nil, fmt.Errorf("Worker rejected %s (HTTP %d)", addr, resp.StatusCode)
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, errors.New("could not establish a secure connection to the Worker relay")
		}
		ws.SetReadLimit(workerFrameSize)
		return &workerConn{ws: ws}, nil
	}
}

// workerConn presents the binary WebSocket messages as a TCP byte stream.
type workerConn struct {
	ws     *websocket.Conn
	reader io.Reader
	write  sync.Mutex
}

func (c *workerConn) Read(p []byte) (int, error) {
	for {
		if c.reader == nil {
			kind, r, err := c.ws.NextReader()
			if websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				return 0, io.EOF
			}
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				return 0, errors.New("the Worker sent a non-binary message")
			}
			c.reader = r
		}
		n, err := c.reader.Read(p)
		if err != io.EOF {
			return n, err
		}
		c.reader = nil
		if n != 0 {
			return n, nil
		}
	}
}

func (c *workerConn) Write(p []byte) (int, error) {
	c.write.Lock()
	defer c.write.Unlock()
	written := 0
	for len(p) != 0 {
		size := min(len(p), workerFrameSize)
		if err := c.ws.WriteMessage(websocket.BinaryMessage, p[:size]); err != nil {
			return written, err
		}
		written += size
		p = p[size:]
	}
	return written, nil
}

func (c *workerConn) Close() error         { return c.ws.Close() }
func (c *workerConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *workerConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }

func (c *workerConn) SetDeadline(t time.Time) error {
	if err := c.SetReadDeadline(t); err != nil {
		return err
	}
	return c.SetWriteDeadline(t)
}

func (c *workerConn) SetReadDeadline(t time.Time) error {
	return c.ws.UnderlyingConn().SetReadDeadline(t)
}

func (c *workerConn) SetWriteDeadline(t time.Time) error {
	c.write.Lock()
	defer c.write.Unlock()
	return c.ws.SetWriteDeadline(t)
}
