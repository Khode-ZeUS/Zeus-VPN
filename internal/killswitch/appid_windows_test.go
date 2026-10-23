package killswitch

import (
	"os"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func appIDFor(t *testing.T, path string) string {
	p, _ := windows.UTF16PtrFromString(path)
	var blob *wtFwpByteBlob
	if err := fwpmGetAppIdFromFileName0(p, unsafe.Pointer(&blob)); err != nil {
		t.Fatal(err)
	}
	defer fwpmFreeMemory0(unsafe.Pointer(&blob))
	b := unsafe.Slice(blob.data, blob.size)
	return windows.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&b[0])), len(b)/2))
}

// A short 8.3 path must resolve to the same app ID the kernel uses.
func TestAppIDLongPath(t *testing.T) {
	exe, _ := os.Executable()
	long, err := longPathName(exe)
	if err != nil {
		t.Fatal(err)
	}
	short := appIDFor(t, exe)
	full := appIDFor(t, long)
	t.Logf("os.Executable: %s", exe)
	t.Logf("app ID (as given): %s", short)
	t.Logf("app ID (long path): %s", full)
	if strings.Contains(full, "~") {
		t.Error("long path still contains a short name")
	}
}
