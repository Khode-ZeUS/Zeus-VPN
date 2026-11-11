package mozilla

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

type signInRoundTripFunc func(*http.Request) (*http.Response, error)

func (f signInRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSignInRouteFallbackBeforeRequestOnly(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantRelay bool
	}{
		{"connection failed", firefoxConnectError{errors.New("dial failed")}, true},
		{"response lost", errors.New("response lost"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			directCalls, relayCalls := 0, 0
			route := &firefoxSignInRoute{
				direct: signInRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					directCalls++
					req.Body.Close()
					return nil, tc.err
				}),
				relay: signInRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					relayCalls++
					if req.Method == http.MethodPost {
						body, err := io.ReadAll(req.Body)
						req.Body.Close()
						if err != nil || string(body) != "{}" {
							t.Fatalf("recreated POST body = %q, %v", body, err)
						}
					}
					return &http.Response{StatusCode: 406, Body: http.NoBody}, nil
				}),
			}
			req, _ := http.NewRequest(http.MethodPost, "https://api.accounts.firefox.com/v1/account/login", strings.NewReader("{}"))
			_, err := route.RoundTrip(req)
			if !tc.wantRelay {
				if !errors.Is(err, tc.err) || relayCalls != 0 {
					t.Fatalf("ambiguous POST was retried: err=%v relay calls=%d", err, relayCalls)
				}
				return
			}
			if err != nil || relayCalls != 1 {
				t.Fatalf("fallback failed: err=%v relay calls=%d", err, relayCalls)
			}
			page, _ := http.NewRequest(http.MethodGet, "https://accounts.firefox.com/", nil)
			if _, err := route.RoundTrip(page); err != nil || directCalls != 1 || relayCalls != 2 {
				t.Fatalf("verification changed routes: err=%v direct=%d relay=%d", err, directCalls, relayCalls)
			}
		})
	}
}

func TestSignInChallengeDoesNotSwitchExit(t *testing.T) {
	directCalls := 0
	route := &firefoxSignInRoute{
		direct: signInRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			directCalls++
			return &http.Response{StatusCode: http.StatusNotAcceptable, Body: http.NoBody}, nil
		}),
		relay: signInRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			t.Fatal("challenge switched to another exit")
			return nil, errors.New("unexpected relay")
		}),
	}
	for _, path := range []string{"/v1/account/login", "/", "/_fs-ch-Test/script.js?reload=true", "/_fs-ch-Test/fst-post-back"} {
		req, _ := http.NewRequest(http.MethodGet, "https://accounts.firefox.com"+path, nil)
		resp, err := route.RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusNotAcceptable {
			t.Fatalf("response changed: %v", err)
		}
	}
	if directCalls != 4 {
		t.Fatalf("direct calls = %d", directCalls)
	}
}

func TestSignInContextKeepsClientAcrossOperations(t *testing.T) {
	ctx := WithCaptchaPrompt(context.Background(), nil)
	first := firefoxAccountsClient(ctx)
	if firefoxAccountsClient(ctx) != first {
		t.Fatal("sign-in recreated its route")
	}
	other := WithCaptchaPrompt(context.Background(), nil)
	if firefoxAccountsClient(other) == first {
		t.Fatal("separate sign-ins share route state")
	}
}
