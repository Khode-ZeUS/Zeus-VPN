// Package netx provides the process-wide outbound dialer. In TUN mode the
// dialer is bound to the physical interface so our own traffic (control plane
// and upstream tunnel) never loops back into the virtual adapter.
package netx

import (
	"context"
	"net"
	"sync"
	"syscall"
	"time"
)

type ControlFunc = func(network, address string, conn syscall.RawConn) error

var (
	mu      sync.RWMutex
	control ControlFunc
)

func SetControl(f ControlFunc) {
	mu.Lock()
	control = f
	mu.Unlock()
}

func DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	mu.RLock()
	c := control
	mu.RUnlock()
	d := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: c}
	return d.DialContext(ctx, network, address)
}
