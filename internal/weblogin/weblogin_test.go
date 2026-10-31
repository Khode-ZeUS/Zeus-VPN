package weblogin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// noRedirect stops http.Get at the receiver's own response (the deliveries
// answer with a 302 to the sign-in page).
func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// deliverQuery builds the URL the desktop delivery navigates to: the
// receiver base, the result query, and this login's nonce appended (the way
// app.go's deliver expression does with the hook's NONCE constant).
func deliverQuery(l *Login, q string) string {
	sep := "?"
	if strings.Contains(q, "?") {
		sep = "&"
	}
	return l.ReceiverURL() + "/" + q + sep + "nonce=" + url.QueryEscape(l.nonce)
}

// The receiver must accept the desktop's nonce-appended delivery, hand the
// tokens to the flow, and send the webview back to the sign-in page. A
// missing nonce (the reverted flow's bug) must stay rejected: the webview
// would otherwise land on the receiver's 403 page instead of signing in.
func TestReceiverDelivery(t *testing.T) {
	l, err := Start()
	if err != nil {
		t.Fatal(err)
	}
	defer l.Stop()

	client := noRedirect()
	res, err := client.Get(deliverQuery(l, "tokens?access_token=at&refresh_token=rt"+
		"&expires_in=3600&email=user%40example.com"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("delivery with the nonce: got HTTP %d, want 302", res.StatusCode)
	}

	select {
	case got := <-l.Result():
		if got.Tokens == nil || got.Tokens.AccessToken != "at" ||
			got.Tokens.RefreshToken != "rt" || got.Email != "user@example.com" {
			t.Fatalf("delivery with the nonce: wrong result %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("delivery with the nonce: no result arrived")
	}

	res, err = client.Get(l.ReceiverURL() + "/tokens?access_token=at")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("delivery without the nonce: got HTTP %d, want 403", res.StatusCode)
	}
}
