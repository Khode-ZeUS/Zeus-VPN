//go:build !windows

package tunnel

import "syscall"

// controlListener mirrors what Go already does on Unix: allow rebinding a
// port whose previous connections are in TIME_WAIT.
func controlListener(network, address string, conn syscall.RawConn) error {
	var serr error
	conn.Control(func(fd uintptr) {
		serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	})
	return serr
}
