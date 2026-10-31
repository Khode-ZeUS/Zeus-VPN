// Package weblogin runs the Firefox Accounts sign-in inside the app's own
// webview. Fastly's edge answers the API password-login route
// (POST /v1/account/login) with a bare HTTP 406 and the block follows the
// client fingerprint, so the password can no longer be submitted from Go —
// while the sign-in web page (browser TLS) passes. The page hands the
// session over through the FxA WebChannel, and exchanges it for OAuth
// tokens from inside the page (the browser's TLS passes networks that drop
// Go's). A shared hook script drives all of it on every platform; the host
// only provides a delivery callback (__zeusDeliver) that carries results to
// the engine.
package weblogin

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"zeusvpn/internal/store"
)

const PageURL = "https://accounts.firefox.com/?context=fx_desktop_v3&service=sync&action=email"

// Result reports the outcome: the finished OAuth tokens (exchanged inside
// the page), the raw session token for the engine to exchange itself, or
// why the flow failed.
type Result struct {
	Tokens       *store.Tokens
	SessionToken string
	Email        string
	Err          error
}

// Login is one web sign-in flow: a loopback receiver plus the pieces the
// platform webview needs (page URL, hook script). The receiver only accepts
// deliveries carrying the login's one-time nonce, and its responses are
// no-store (tokens must never land in a cache).
type Login struct {
	ln       net.Listener
	receiver string
	nonce    string
	result   chan Result
	mu       sync.Mutex
	stopped  bool
}

// Start opens the loopback receiver.
func Start() (*Login, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not open the sign-in receiver: %w", err)
	}
	nb := make([]byte, 16)
	if _, err := rand.Read(nb); err != nil {
		return nil, fmt.Errorf("could not generate the sign-in nonce: %w", err)
	}
	l := &Login{ln: ln, receiver: "http://" + ln.Addr().String(),
		nonce: hex.EncodeToString(nb), result: make(chan Result, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("/tokens", l.guard(l.token))
	mux.HandleFunc("/session", l.guard(l.session))
	mux.HandleFunc("/cancel", l.guard(l.cancel))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5}
	go srv.Serve(ln)
	return l, nil
}

// guard rejects deliveries without this login's nonce and marks every
// response uncacheable.
func (l *Login) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("nonce") != l.nonce {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		next(w, r)
	}
}

// Stop closes the receiver; the flow is over either way.
func (l *Login) Stop() {
	l.mu.Lock()
	l.stopped = true
	l.mu.Unlock()
	l.ln.Close()
}

func (l *Login) deliver(r Result) {
	l.mu.Lock()
	stopped := l.stopped
	l.mu.Unlock()
	if stopped {
		return
	}
	select {
	case l.result <- r:
	default:
	}
}

func writeDelivered(w http.ResponseWriter) {
	// Send the webview back to the sign-in page; the platform will take it
	// to the app UI once the handover finishes.
	w.Header().Set("Location", PageURL)
	w.WriteHeader(302)
}

// token handles the session handover: either the finished OAuth tokens
// (delivered by the in-page exchange) or the raw session token (delivered
// by platforms whose in-page exchange failed, for the engine to retry).
func (l *Login) token(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	email := q.Get("email")

	if q.Get("kind") == "tokens" {
		at := q.Get("access_token")
		expires, _ := strconv.Atoi(q.Get("expires_in"))
		if expires <= 0 {
			expires = 86400
		}
		tokens := store.Tokens{AccessToken: at, RefreshToken: q.Get("refresh_token"),
			Scope: "profile https://identity.mozilla.com/apps/vpn", ObtainedAt: time.Now(), ExpiresIn: int64(expires)}
		l.deliver(Result{Tokens: &tokens, Email: email})
		writeDelivered(w)
		return
	}

	token := q.Get("token")
	if !sessionTokenHex.MatchString(token) {
		w.WriteHeader(400)
		return
	}
	verified := q.Get("verified") != "false"
	if verified {
		l.deliver(Result{SessionToken: token, Email: email})
	}
	writeDelivered(w)
}

// session handles the fallback handover of a session (the in-page exchange
// failed; the engine exchanges it from its own path).
func (l *Login) session(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	token := q.Get("token")
	if !sessionTokenHex.MatchString(token) {
		w.WriteHeader(400)
		return
	}
	l.deliver(Result{SessionToken: token, Email: q.Get("email")})
	w.WriteHeader(200)
}

// cancel handles the user pressing the injected cancel button.
func (l *Login) cancel(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(200)
	l.deliver(Result{Err: errors.New("canceled")})
}

// Result delivers the outcome of the flow.
func (l *Login) Result() <-chan Result { return l.result }

// HookJS returns the shared in-page sign-in hook with the given delivery
// expression (see hook.go).
func (l *Login) HookJS(deliver string) string { return HookJS(deliver, l.nonce) }

// ReceiverURL is the base URL of the loopback receiver (for platform// ReceiverURL is the base URL of the loopback receiver (for platform
// delivery adapters that navigate to it).
func (l *Login) ReceiverURL() string { return l.receiver }

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
