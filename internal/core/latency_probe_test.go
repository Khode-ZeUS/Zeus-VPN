//go:build e2e

package core

import "testing"

// Live network check: pings must return real milliseconds for working
// servers, not timeouts.
func TestLatencyProbe(t *testing.T) {
	e := NewEngine()
	m := e.Latency()
	if len(m) == 0 {
		t.Fatal("no latency results")
	}
	ok, fail := 0, 0
	for k, v := range m {
		if v >= 0 {
			ok++
		} else {
			fail++
			t.Logf("timeout: %s", k)
		}
	}
	t.Logf("%d ok, %d timeouts", ok, fail)
	if ok == 0 {
		t.Fatal("every server timed out — probe is broken")
	}
}
