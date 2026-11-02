package mozilla

import (
	"errors"
	"testing"
)

// fakeBootstrap builds a manager over in-memory paths and memory.
func fakeBootstrap(sig string, fail map[string]bool) (*Bootstrap, map[string]int) {
	calls := map[string]int{}
	mem := bootstrapMemory{Network: sig}
	b := NewBootstrap(BootstrapOptions{
		Sig:  func() string { return sig },
		Save: func(m bootstrapMemory) { mem = m },
		Load: func() (bootstrapMemory, bool) { return mem, mem.Network == sig },
		Cache: func() ([]Country, bool) {
			calls["cache"]++
			if fail["cache"] {
				return nil, false
			}
			return []Country{{Name: "Cached", Code: "CA"}}, true
		},
	})
	// The direct and proxy paths are faked after construction so the calls
	// counter sees them; the cache option keeps its own closure.
	for i := range b.paths {
		if b.paths[i].name == "cache" {
			continue
		}
		p := b.paths[i]
		b.paths[i].fetch = func() ([]Country, error) {
			calls[p.name]++
			if fail[p.name] {
				return nil, errors.New("blocked on this network")
			}
			return []Country{{Name: "List", Code: "LI"}}, nil
		}
	}
	return b, calls
}

// The paths run in order, a failing path sits out its cooldown on the same
// network, and a remembered-good path jumps the queue on the next call.
func TestBootstrapPathsAndMemory(t *testing.T) {
	b, calls := fakeBootstrap("net1", map[string]bool{"direct": true})
	list, reports, err := b.ServerList()
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Code != "CA" {
		t.Fatalf("got list from %v, want the cache fallback", reports)
	}
	if len(reports) != 2 || reports[0].Path != "direct" || reports[1].Path != "cache" {
		t.Fatalf("cache should be the last resort: %+v", reports)
	}
	if calls["direct"] != 1 {
		t.Fatalf("direct attempted %d times, want 1 (it sits out its cooldown)", calls["direct"])
	}

	// Same network, next call: direct is still in cooldown, cache remembered.
	list2, _, err := b.ServerList()
	if err != nil || list2[0].Code != "CA" {
		t.Fatalf("second call: %v %v", list2, err)
	}
	if calls["direct"] != 1 {
		t.Fatalf("direct ran again inside its cooldown")
	}

	// A different network: the memory resets, direct is tried again.
	b2, calls2 := fakeBootstrap("net2", nil)
	list3, _, err := b2.ServerList()
	if err != nil || list3[0].Code != "LI" {
		t.Fatalf("new network: %v %v", list3, err)
	}
	if calls2["direct"] != 1 {
		t.Fatalf("direct should be the first path on a fresh network")
	}

	// And the winner is remembered: the next call goes straight to it.
	if _, _, err := b2.ServerList(); err != nil {
		t.Fatal(err)
	}
	if calls2["proxy"] != 0 || calls2["masque"] != 0 || calls2["cache"] != 0 {
		t.Fatalf("the remembered path should answer alone: %v", calls2)
	}
	if calls2["direct"] != 2 {
		t.Fatalf("direct calls = %d, want 2", calls2["direct"])
	}
}
