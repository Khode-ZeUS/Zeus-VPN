package netdiag

import (
	"crypto/x509"
	"errors"
	"net"
	"testing"
	"time"
)

// A connection error must land on its own diagnostics code: DNS failures,
// TCP failures, certificate verification failures, timeouts and TLS
// failures each mean a different stage broke.
func TestClassify(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "host"}
	opDial := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	opWrite := &net.OpError{Op: "write", Err: errors.New("broken pipe")}
	timeout := errTimeout{}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, OK},
		{"dns", dnsErr, DNSFailed},
		{"wrapped dns", &net.OpError{Op: "dial", Err: dnsErr}, DNSFailed},
		{"tcp dial", opDial, TCPFailed},
		{"tls write", opWrite, TLSFailed},
		{"timeout", timeout, ConnTimeout},
		{"unknown authority", x509.UnknownAuthorityError{}, CertInvalid},
		{"other", errors.New("some tls error"), TLSFailed},
	}
	for _, c := range cases {
		if got, _ := Classify(c.err); got != c.want {
			t.Errorf("%s: Classify = %q, want %q", c.name, got, c.want)
		}
	}
}

type errTimeout struct{}

func (errTimeout) Error() string   { return "i/o timeout" }
func (errTimeout) Timeout() bool   { return true }
func (errTimeout) Temporary() bool { return true }

// The diagnostics buffer keeps the newest events and reports the network
// the host announced.
func TestReportAndSnapshot(t *testing.T) {
	Clear()
	SetNetwork("mobile", "HamrahAval")
	for i := 0; i < keep+50; i++ {
		Report("host", TLS, OK, "", 10*time.Millisecond)
	}
	snap := Snapshot()
	if len(snap) != keep {
		t.Fatalf("snapshot holds %d events, want %d", len(snap), keep)
	}
	last := snap[len(snap)-1]
	if last.Network != "mobile" || last.Operator != "HamrahAval" || last.Stage != TLS || last.Code != OK {
		t.Fatalf("last event wrong: %+v", last)
	}
	Clear()
	if len(Snapshot()) != 0 {
		t.Fatal("Clear left events behind")
	}
}
