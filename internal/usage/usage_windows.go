package usage

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	iphlpapi = windows.NewLazySystemDLL("iphlpapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	procGetExtendedTcpTable        = iphlpapi.NewProc("GetExtendedTcpTable")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
)

const (
	afInet              = 2
	afInet6             = 23
	tcpTableOwnerPIDAll = 5
	processQueryLimited = 0x1000
	tcpStateListen      = 2
)

// mibTcpRowOwnerPid is MIB_TCPROW_OWNER_PID (IPv4).
type mibTcpRowOwnerPid struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	PID        uint32
}

// mibTcp6RowOwnerPid is MIB_TCP6ROW_OWNER_PID (IPv6): addresses come first.
type mibTcp6RowOwnerPid struct {
	LocalAddr  [16]byte
	LocalPort  uint32
	RemoteAddr [16]byte
	RemotePort uint32
	State      uint32
	PID        uint32
}

// ownerCache maps a local TCP port (v4 and v6) to the owning process's
// name, rebuilt at most twice a second: several connections of one app
// arrive together and the table query is not free.
var (
	ownerMu    sync.Mutex
	ownerCache = map[uint16]string{}
	ownerAt    time.Time
)

// owner names the process that owns a local address ("ip:port"), or "".
func owner(local string) string {
	ap, err := netip.ParseAddrPort(local)
	if err != nil {
		return ""
	}
	port := uint16(ap.Port())

	ownerMu.Lock()
	defer ownerMu.Unlock()
	if time.Since(ownerAt) > 500*time.Millisecond {
		refreshOwners()
	}
	if name, ok := ownerCache[port]; ok {
		return name
	}
	// The connection may be newer than the cache: one forced re-read,
	// then give up (unresolvable connections stay uncounted).
	refreshOwners()
	return ownerCache[port]
}

// refreshOwners snapshots the established TCP connections (IPv4 and IPv6)
// and their owning processes. Must be called with ownerMu held.
func refreshOwners() {
	ownerAt = time.Now()
	next := make(map[uint16]string)

	nameCache := map[uint32]string{}
	add := func(pid uint32, netPort uint32) {
		if pid == 0 {
			return
		}
		// The port arrives big-endian in the low 16 bits of the DWORD:
		// ntohs it (low byte of the DWORD is the high byte of the port).
		p := uint16(netPort&0xff)<<8 | uint16(netPort>>8&0xff)
		if _, taken := next[p]; taken {
			return
		}
		name, ok := nameCache[pid]
		if !ok {
			name = processName(pid)
			nameCache[pid] = name
		}
		if name != "" {
			next[p] = name
		}
	}

	// table fetches one family's TCP table and walks its rows.
	table := func(af uint32, rowSize uintptr, row func(buf []byte) (state, pid, netPort uint32)) {
		var size uint32
		// The first call reports the needed size and "fails" with
		// ERROR_INSUFFICIENT_BUFFER (some Windows versions report
		// ERROR_MORE_DATA); only then can the table be fetched.
		r, _, _ := procGetExtendedTcpTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDAll, 0)
		if r != uintptr(windows.ERROR_INSUFFICIENT_BUFFER) && r != uintptr(windows.ERROR_MORE_DATA) {
			return
		}
		if size == 0 || size > 64<<20 {
			return
		}
		buf := make([]byte, size)
		r, _, _ = procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, uintptr(af), tcpTableOwnerPIDAll, 0)
		if r != 0 || len(buf) < 4 {
			return
		}
		rows := binary.LittleEndian.Uint32(buf)
		for i := uint32(0); i < rows; i++ {
			off := 4 + uintptr(i)*rowSize
			if off+rowSize > uintptr(len(buf)) {
				break
			}
			state, pid, netPort := row(buf[off : off+rowSize])
			if state == tcpStateListen {
				continue
			}
			add(pid, netPort)
		}
	}

	table(afInet, unsafe.Sizeof(mibTcpRowOwnerPid{}), func(b []byte) (uint32, uint32, uint32) {
		r := *(*mibTcpRowOwnerPid)(unsafe.Pointer(&b[0]))
		return r.State, r.PID, r.LocalPort
	})
	table(afInet6, unsafe.Sizeof(mibTcp6RowOwnerPid{}), func(b []byte) (uint32, uint32, uint32) {
		r := *(*mibTcp6RowOwnerPid)(unsafe.Pointer(&b[0]))
		return r.State, r.PID, r.LocalPort
	})

	ownerCache = next
}

// processName returns the executable's file name (e.g. "chrome.exe"), or "".
func processName(pid uint32) string {
	h, _, _ := procOpenProcess.Call(processQueryLimited, 0, uintptr(pid))
	if h == 0 {
		return ""
	}
	defer procCloseHandle.Call(h)
	var buf [1024]uint16
	size := uint32(len(buf))
	r, _, _ := procQueryFullProcessImageNameW.Call(h, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r == 0 || size == 0 {
		return ""
	}
	path := windows.UTF16ToString(buf[:size])
	if i := strings.LastIndexByte(path, '\\'); i >= 0 {
		path = path[i+1:]
	}
	return path
}
