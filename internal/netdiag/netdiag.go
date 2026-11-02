// Package netdiag records what happens to the app's outbound connections,
// stage by stage — DNS, TCP, TLS, certificate, HTTP — so a failing network
// can be diagnosed without guesswork. Every recorded event carries one
// distinct error code (DNS_FAILED, TCP_FAILED, TLS_FAILED, CERT_INVALID,
// HTTP_BLOCKED, CONNECTION_TIMEOUT) plus the stage duration, and the current
// network kind and operator when the host supplies them.
package netdiag

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Stage is one step of an outbound connection.
type Stage string

const (
	DNS  Stage = "DNS"
	TCP  Stage = "TCP"
	TLS  Stage = "TLS"
	Cert Stage = "CERT"
	HTTP Stage = "HTTP"
)

// Error codes: one distinct code per distinct failure.
const (
	OK          = "OK"
	DNSFailed   = "DNS_FAILED"
	TCPFailed   = "TCP_FAILED"
	TLSFailed   = "TLS_FAILED"
	CertInvalid = "CERT_INVALID"
	HTTPBlocked = "HTTP_BLOCKED"
	ConnTimeout = "CONNECTION_TIMEOUT"
)

// Event is one recorded stage of one connection attempt.
type Event struct {
	At       time.Time `json:"at"`
	Target   string    `json:"target"`
	Stage    Stage     `json:"stage"`
	Code     string    `json:"code"`
	Detail   string    `json:"detail,omitempty"`
	Millis   int64     `json:"ms"`
	Network  string    `json:"network"`
	Operator string    `json:"operator,omitempty"`
}

const keep = 400

var (
	mu       sync.Mutex
	events   []Event
	kind     atomic.Value // string
	operator atomic.Value // string
)

func init() {
	kind.Store("unknown")
	operator.Store("")
}

// SetNetwork tells the diagnostics which network the device is on. The host
// calls it when the connectivity changes (Android: Wi-Fi vs mobile data and
// the carrier; desktop: "wifi" with an unknown operator is the best guess).
func SetNetwork(networkKind, carrier string) {
	if networkKind != "" {
		kind.Store(networkKind)
	}
	if carrier != "" {
		operator.Store(carrier)
	}
}

// Report records one finished stage.
func Report(target string, stage Stage, code, detail string, took time.Duration) {
	ev := Event{
		At:       time.Now(),
		Target:   target,
		Stage:    stage,
		Code:     code,
		Detail:   detail,
		Millis:   took.Milliseconds(),
		Network:  kind.Load().(string),
		Operator: operator.Load().(string),
	}
	mu.Lock()
	events = append(events, ev)
	if n := len(events); n > keep {
		events = events[n-keep:]
	}
	mu.Unlock()
}

// Snapshot returns the recent events, oldest first.
func Snapshot() []Event {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Event, len(events))
	copy(out, events)
	return out
}

// Clear forgets the recorded events (Reset Network Configuration).
func Clear() {
	mu.Lock()
	events = nil
	mu.Unlock()
}

// Classify maps a connection error onto its diagnostics code: DNS failures,
// TCP failures, TLS handshake failures, certificate verification failures
// and timeouts each get their own code, per the diagnostics plan.
func Classify(err error) (code, detail string) {
	if err == nil {
		return OK, ""
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return DNSFailed, dnsErr.Err
	}
	var certErr *tls.CertificateVerificationError
	if errors.As(err, &certErr) {
		return CertInvalid, certErr.Error()
	}
	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return CertInvalid, unknown.Error()
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return CertInvalid, hostErr.Error()
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return ConnTimeout, err.Error()
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			return TCPFailed, opErr.Err.Error()
		}
		return TLSFailed, err.Error()
	}
	return TLSFailed, err.Error()
}
