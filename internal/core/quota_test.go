package core

import (
	"testing"

	"zeusvpn/internal/mozilla"
)

func TestQuotaKeepsLocalUsageUntilGuardianAccountsForIt(t *testing.T) {
	var q quotaEstimate
	pass := mozilla.ProxyPass{QuotaLimit: 1000, QuotaLeft: 1000, QuotaReset: "this month"}
	q.update(&pass, "account", 0, 40)
	q.update(&pass, "account", 100, 40)
	if got := q.remaining(150); got != 810 {
		t.Fatalf("unchanged Guardian quota reset local usage: %d", got)
	}

	pass.QuotaLeft = 940
	q.update(&pass, "account", 150, 0)
	if got := q.remaining(160); got != 800 {
		t.Fatalf("partial server update double-counted or dropped usage: %d", got)
	}

	pass.QuotaLeft, pass.QuotaReset = 1000, "next month"
	q.update(&pass, "account", 160, 0)
	if got := q.remaining(160); got != 1000 {
		t.Fatalf("new quota period did not reset usage: %d", got)
	}
	q.update(&mozilla.ProxyPass{}, "other account", 160, 0)
	if q.limit != 0 {
		t.Fatal("the previous account's quota carried over")
	}
}
