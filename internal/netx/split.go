package netx

import (
	"encoding/binary"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// TLS ClientHello fragmentation: sending the handshake in several TCP
// segments hides the server name (SNI) from filters that only inspect the
// first packet.
const (
	SplitAuto      = "auto" // plain first; fragment hosts whose handshake gets cut off
	SplitOff       = "off"
	SplitSNI       = "sni"        // cut the record in the middle of the server name
	SplitFirstByte = "first_byte" // send the first byte on its own
)

var (
	splitMode atomic.Value // string
	// blocked remembers hosts whose plain handshake was interrupted.
	blocked sync.Map // host -> time.Time
)

const blockedMemory = 30 * time.Minute

func init() { splitMode.Store(SplitAuto) }

func SetSplitMode(m string) {
	switch m {
	case SplitOff, SplitSNI, SplitFirstByte:
	default:
		m = SplitAuto
	}
	splitMode.Store(m)
}

func SplitMode() string { return splitMode.Load().(string) }

// HostBlocked reports whether a plain handshake to host was recently cut
// off, i.e. a retry will use fragmentation in auto mode.
func HostBlocked(host string) bool {
	if t, ok := blocked.Load(host); ok {
		if time.Since(t.(time.Time)) < blockedMemory {
			return true
		}
		blocked.Delete(host)
	}
	return false
}

// MarkBlocked records that a plain handshake to host was cut off, so the
// next attempt will use ClientHello fragmentation (SplitSNI). Used by the
// latency probe so that a server whose plain handshake is filtered (like
// Canada) is still probed with fragmentation, exactly like a real connection.
func MarkBlocked(host string) {
	blocked.Store(host, time.Now())
}

type splitConn struct {
	net.Conn
	wrote     bool
	host      string
	plainSent bool
	gotData   bool
}

func wrapSplit(c net.Conn) net.Conn {
	if SplitMode() == SplitOff {
		return c
	}
	return &splitConn{Conn: c}
}

func (c *splitConn) Write(b []byte) (int, error) {
	if c.wrote || !looksLikeClientHello(b) {
		c.wrote = true
		return c.Conn.Write(b)
	}
	c.wrote = true
	sniStart, sniEnd := findSNI(b)
	if sniStart > 0 {
		c.host = string(b[sniStart:sniEnd])
	}
	mode := SplitMode()
	if mode == SplitAuto {
		if c.host != "" && HostBlocked(c.host) {
			mode = SplitSNI
		} else {
			c.plainSent = true
			return c.Conn.Write(b)
		}
	}
	var cuts []int
	switch {
	case mode == SplitSNI && sniStart > 0:
		cuts = []int{sniStart, sniStart + 1}
		if mid := (sniStart + sniEnd) / 2; mid > sniStart+1 {
			cuts = append(cuts, mid)
		}
	default:
		cuts = []int{1}
	}
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		tc.SetNoDelay(true)
	}
	prev := 0
	for _, cut := range append(cuts, len(b)) {
		if _, err := c.Conn.Write(b[prev:cut]); err != nil {
			return prev, err
		}
		prev = cut
	}
	return len(b), nil
}

// Read notices a plain handshake that the network cut off before the server
// answered, so the next attempt to the same host is fragmented.
func (c *splitConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.gotData = true
	} else if err != nil && !c.gotData && c.plainSent && c.host != "" {
		blocked.Store(c.host, time.Now())
	}
	return n, err
}

func (c *splitConn) CloseWrite() error {
	if tc, ok := c.Conn.(*net.TCPConn); ok {
		return tc.CloseWrite()
	}
	return nil
}

func looksLikeClientHello(b []byte) bool {
	return len(b) > 5 && b[0] == 0x16 && b[1] == 0x03 && b[5] == 0x01
}

// findSNI returns the byte range of the server name inside a ClientHello record.
func findSNI(b []byte) (int, int) {
	if len(b) < 44 {
		return 0, 0
	}
	p := 5 + 4 + 2 + 32
	if p >= len(b) {
		return 0, 0
	}
	p += 1 + int(b[p])
	if p+2 > len(b) {
		return 0, 0
	}
	p += 2 + int(binary.BigEndian.Uint16(b[p:]))
	if p >= len(b) {
		return 0, 0
	}
	p += 1 + int(b[p])
	if p+2 > len(b) {
		return 0, 0
	}
	end := p + 2 + int(binary.BigEndian.Uint16(b[p:]))
	p += 2
	if end > len(b) {
		end = len(b)
	}
	for p+4 <= end {
		typ := binary.BigEndian.Uint16(b[p:])
		l := int(binary.BigEndian.Uint16(b[p+2:]))
		body := p + 4
		if body+l > end {
			return 0, 0
		}
		if typ == 0 && l >= 5 && b[body+2] == 0 {
			nameLen := int(binary.BigEndian.Uint16(b[body+3:]))
			start := body + 5
			if start+nameLen <= body+l {
				return start, start + nameLen
			}
			return 0, 0
		}
		p = body + l
	}
	return 0, 0
}
