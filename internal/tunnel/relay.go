package tunnel

import (
	"io"
	"net"
	"sync"
	"time"
)

var bufPool = sync.Pool{New: func() any { b := make([]byte, 32*1024); return &b }}

type closeWriter interface{ CloseWrite() error }

// Relay copies both directions until both sides finish. When one direction
// ends cleanly the other side gets a half-close and a grace period to drain.
func Relay(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			a.Close()
			b.Close()
		})
	}
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		bp := bufPool.Get().(*[]byte)
		_, err := io.CopyBuffer(dst, src, *bp)
		bufPool.Put(bp)
		if err != nil {
			closeBoth()
			return
		}
		if cw, ok := dst.(closeWriter); ok {
			cw.CloseWrite()
		} else {
			closeBoth()
			return
		}
		time.AfterFunc(2*time.Minute, closeBoth)
	}
	go pipe(b, a)
	go pipe(a, b)
	wg.Wait()
	closeBoth()
}
