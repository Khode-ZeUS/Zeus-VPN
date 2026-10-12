package vpn

import (
	"bytes"
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// serverFirstPorts are protocols where the server talks first, so waiting
// for client bytes would only add latency.
var serverFirstPorts = map[uint16]bool{21: true, 22: true, 23: true, 25: true, 110: true, 143: true, 587: true, 3306: true, 5432: true}

// sniff reads the first client bytes and extracts the destination domain from
// a TLS ClientHello (SNI) or an HTTP Host header. Connecting by name lets the
// VPN server resolve it, which defeats local DNS poisoning. The bytes consumed
// are returned so they can be replayed upstream.
func sniff(conn net.Conn, port uint16) (string, []byte) {
	if serverFirstPorts[port] {
		return "", nil
	}
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	defer conn.SetReadDeadline(time.Time{})
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	for len(buf) < 16*1024 {
		n, err := conn.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if domain, done := parseFirstBytes(buf); done {
			return domain, buf
		}
		if err != nil {
			break
		}
	}
	return "", buf
}

// parseFirstBytes returns done=true once no more data could change the answer.
func parseFirstBytes(b []byte) (string, bool) {
	if len(b) == 0 {
		return "", false
	}
	if b[0] == 0x16 {
		if len(b) < 5 {
			return "", false
		}
		recLen := int(binary.BigEndian.Uint16(b[3:5]))
		if len(b) < 5+recLen {
			return "", false
		}
		return parseSNI(b[5 : 5+recLen]), true
	}
	if isHTTPMethodPrefix(b) {
		end := bytes.Index(b, []byte("\r\n\r\n"))
		if end < 0 && len(b) < 8192 {
			return "", false
		}
		return parseHTTPHost(b), true
	}
	return "", true
}

func isHTTPMethodPrefix(b []byte) bool {
	for _, m := range []string{"GET ", "POST ", "HEAD ", "PUT ", "DELETE ", "OPTIONS ", "PATCH ", "CONNECT ", "TRACE "} {
		if len(b) >= len(m) && string(b[:len(m)]) == m {
			return true
		}
	}
	return false
}

func parseHTTPHost(b []byte) string {
	for _, line := range strings.Split(string(b), "\r\n")[1:] {
		if line == "" {
			break
		}
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "host") {
			host := strings.TrimSpace(v)
			if h, _, err := net.SplitHostPort(host); err == nil {
				host = h
			}
			return validDomain(host)
		}
	}
	return ""
}

func parseSNI(hs []byte) string {
	// Handshake: type(1) len(3) version(2) random(32)
	if len(hs) < 38 || hs[0] != 1 {
		return ""
	}
	p := 38
	skip := func(lenBytes int) bool {
		if p+lenBytes > len(hs) {
			return false
		}
		var l int
		for i := 0; i < lenBytes; i++ {
			l = l<<8 | int(hs[p+i])
		}
		p += lenBytes + l
		return p <= len(hs)
	}
	if !skip(1) || !skip(2) || !skip(1) { // session id, cipher suites, compression
		return ""
	}
	if p+2 > len(hs) {
		return ""
	}
	extEnd := p + 2 + int(binary.BigEndian.Uint16(hs[p:]))
	p += 2
	if extEnd > len(hs) {
		extEnd = len(hs)
	}
	for p+4 <= extEnd {
		typ := binary.BigEndian.Uint16(hs[p:])
		l := int(binary.BigEndian.Uint16(hs[p+2:]))
		p += 4
		if p+l > extEnd {
			return ""
		}
		if typ == 0 && l >= 5 {
			ext := hs[p : p+l]
			nameLen := int(binary.BigEndian.Uint16(ext[3:5]))
			if ext[2] == 0 && 5+nameLen <= len(ext) {
				return validDomain(string(ext[5 : 5+nameLen]))
			}
			return ""
		}
		p += l
	}
	return ""
}

func validDomain(s string) string {
	s = strings.TrimSuffix(strings.ToLower(s), ".")
	if s == "" || len(s) > 253 || net.ParseIP(s) != nil || !strings.Contains(s, ".") {
		return ""
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_') {
			return ""
		}
	}
	return s
}
