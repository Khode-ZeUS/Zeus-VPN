//go:build windows

package tunnel

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// controlListener sets SO_REUSEADDR so the local proxy can rebind its port
// immediately after a disconnect, while the previous connections are still
// in TIME_WAIT (Windows refuses the bind otherwise).
func controlListener(network, address string, conn syscall.RawConn) error {
	var serr error
	conn.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
	return serr
}
