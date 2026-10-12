package tunnel

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DialFunc opens a TCP connection to "host:port" (through the tunnel, or
// directly for bypassed destinations).
type DialFunc func(ctx context.Context, target string) (net.Conn, error)

// MixedServer is a local proxy speaking SOCKS5, SOCKS4/4a and HTTP (CONNECT
// and absolute-URI requests) on a single port.
type MixedServer struct {
	ln      net.Listener
	dial    DialFunc
	forward RoundTripFunc
	log     func(string)

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup
}

func ListenMixed(addr string, dial DialFunc, forward RoundTripFunc, log func(string)) (*MixedServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("could not open local proxy port %s: %w", addr, err)
	}
	s := &MixedServer{ln: ln, dial: dial, forward: forward, log: log, conns: map[net.Conn]struct{}{}}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

func (s *MixedServer) Addr() string { return s.ln.Addr().String() }

func (s *MixedServer) Close() {
	s.ln.Close()
	s.mu.Lock()
	for c := range s.conns {
		c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *MixedServer) serve() {
	defer s.wg.Done()
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.conns[c] = struct{}{}
		s.mu.Unlock()
		go func() {
			defer func() {
				s.mu.Lock()
				delete(s.conns, c)
				s.mu.Unlock()
				c.Close()
			}()
			s.handle(c)
		}()
	}
}

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func (p *peekConn) CloseWrite() error {
	if cw, ok := p.Conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return nil
}

func (s *MixedServer) handle(c net.Conn) {
	c.SetReadDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(c)
	first, err := br.Peek(1)
	if err != nil {
		return
	}
	pc := &peekConn{Conn: c, r: br}
	switch first[0] {
	case 5:
		s.handleSOCKS5(pc, br)
	case 4:
		s.handleSOCKS4(pc, br)
	default:
		s.handleHTTP(pc, br)
	}
}

func (s *MixedServer) connect(client net.Conn, target string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	up, err := s.dial(ctx, target)
	if err != nil {
		s.log(fmt.Sprintf("warning failed to open tunnel to %s: %v", target, err))
	}
	return up, err
}

func (s *MixedServer) handleSOCKS5(c *peekConn, br *bufio.Reader) {
	hdr := make([]byte, 2)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return
	}
	methods := make([]byte, hdr[1])
	if _, err := io.ReadFull(br, methods); err != nil {
		return
	}
	c.Write([]byte{5, 0})
	req := make([]byte, 4)
	if _, err := io.ReadFull(br, req); err != nil {
		return
	}
	reply := func(code byte) { c.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0}) }
	if req[1] != 1 {
		reply(7) // command not supported (only CONNECT)
		return
	}
	var host string
	switch req[3] {
	case 1:
		ip := make([]byte, 4)
		if _, err := io.ReadFull(br, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 4:
		ip := make([]byte, 16)
		if _, err := io.ReadFull(br, ip); err != nil {
			return
		}
		host = net.IP(ip).String()
	case 3:
		l, err := br.ReadByte()
		if err != nil {
			return
		}
		name := make([]byte, l)
		if _, err := io.ReadFull(br, name); err != nil {
			return
		}
		host = string(name)
	default:
		reply(8)
		return
	}
	pb := make([]byte, 2)
	if _, err := io.ReadFull(br, pb); err != nil {
		return
	}
	target := net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(pb))))
	up, err := s.connect(c, target)
	if err != nil {
		var re *RefusedError
		if errors.As(err, &re) {
			reply(2)
		} else {
			reply(5)
		}
		return
	}
	reply(0)
	c.SetReadDeadline(time.Time{})
	Relay(c, up)
}

func (s *MixedServer) handleSOCKS4(c *peekConn, br *bufio.Reader) {
	hdr := make([]byte, 8)
	if _, err := io.ReadFull(br, hdr); err != nil || hdr[1] != 1 {
		return
	}
	port := binary.BigEndian.Uint16(hdr[2:4])
	ip := net.IP(hdr[4:8])
	if _, err := br.ReadString(0); err != nil { // user id
		return
	}
	host := ip.String()
	if ip[0] == 0 && ip[1] == 0 && ip[2] == 0 && ip[3] != 0 { // SOCKS4a
		name, err := br.ReadString(0)
		if err != nil {
			return
		}
		host = strings.TrimSuffix(name, "\x00")
	}
	up, err := s.connect(c, net.JoinHostPort(host, strconv.Itoa(int(port))))
	if err != nil {
		c.Write([]byte{0, 91, 0, 0, 0, 0, 0, 0})
		return
	}
	c.Write([]byte{0, 90, 0, 0, 0, 0, 0, 0})
	c.SetReadDeadline(time.Time{})
	Relay(c, up)
}

var hopHeaders = []string{"Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate", "Connection", "Keep-Alive", "TE", "Trailer", "Upgrade"}

func (s *MixedServer) handleHTTP(c *peekConn, br *bufio.Reader) {
	req, err := http.ReadRequest(br)
	if err != nil {
		return
	}
	if req.Method == http.MethodConnect {
		target := req.Host
		if _, _, err := net.SplitHostPort(target); err != nil {
			target = net.JoinHostPort(target, "443")
		}
		up, err := s.connect(c, target)
		if err != nil {
			fmt.Fprintf(c, "HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		fmt.Fprintf(c, "HTTP/1.1 200 Connection Established\r\n\r\n")
		c.SetReadDeadline(time.Time{})
		Relay(c, up)
		return
	}

	c.SetReadDeadline(time.Time{})
	ServeHTTPForward(c, br, req, "", func(r *http.Request) (*http.Response, error) {
		resp, err := s.forward(r)
		if err != nil {
			s.log(fmt.Sprintf("warning http request to %s failed: %v", r.URL.Host, err))
		}
		return resp, err
	})
}
