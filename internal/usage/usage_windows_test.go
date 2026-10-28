package usage

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// Wrap must attribute a real loopback connection to this test process.
func TestWrapAttributesOwner(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		io.Copy(io.Discard, c)
		c.Close()
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	w := Wrap(conn)
	if _, ok := w.(*countingConn); !ok {
		t.Fatalf("connection was not attributed to a process (owner lookup failed)")
	}
	if _, err := w.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	list := ListApps()
	if len(list) == 0 {
		t.Fatal("no usage recorded")
	}
	found := false
	for _, a := range list {
		t.Logf("app %s: up=%d down=%d", a.Name, a.Up, a.Down)
		if strings.HasSuffix(a.Name, ".test") || strings.HasSuffix(a.Name, ".exe") {
			// Bytes written to the app count as its download.
			found = a.Down >= 5 || found
		}
	}
	if !found {
		t.Fatal("this test process's usage was not recorded")
	}
}
