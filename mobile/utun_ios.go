//go:build ios

package zeuscore

import "golang.org/x/sys/unix"

// findTunFD finds the utun socket of the iOS network extension, which
// NEPacketTunnelProvider does not expose: it is the control socket
// connected to com.apple.net.utun_control.
func findTunFD() int {
	var info unix.CtlInfo
	copy(info.Name[:], "com.apple.net.utun_control")
	for fd := 0; fd < 1024; fd++ {
		sa, err := unix.Getpeername(fd)
		if err != nil {
			continue
		}
		addr, ok := sa.(*unix.SockaddrCtl)
		if !ok {
			continue
		}
		if info.Id == 0 {
			if err := unix.IoctlCtlInfo(fd, &info); err != nil {
				continue
			}
		}
		if addr.ID == info.Id {
			return fd
		}
	}
	return -1
}
