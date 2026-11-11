package mozilla

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func TestPasswordLoginContinuesAfterHumanCaptcha(t *testing.T) {
	const image = "data:image/png;base64,AAAA"
	const prefix = "/_fs-ch-Test"
	client := Client()
	originalTransport, originalJar := client.Transport, client.Jar
	defer func() { client.Transport, client.Jar = originalTransport, originalJar }()
	client.Jar, _ = cookiejar.New(nil)
	prompted, answered := false, false
	client.Transport = loginRoundTripper(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, ""
		headers := make(http.Header)
		switch req.URL.Path {
		case "/v1/account/login":
			if _, err := req.Cookie("captcha-test"); err != nil {
				status = http.StatusNotAcceptable
			} else {
				body = `{"sessionToken":"` + strings.Repeat("ab", 32) + `","verified":true}`
			}
		case "/":
			if _, err := req.Cookie("captcha-test"); err != nil {
				body = "Client Challenge " + prefix
			}
		case prefix + "/script.js":
			body = `init([{"ty":"captcha","data":{"image_b64":"` + image + `"}}], "token-1", "` + prefix + `");`
		case prefix + "/fst-post-back":
			var post struct {
				Token string
				Data  []map[string]any
			}
			if err := json.NewDecoder(req.Body).Decode(&post); err != nil ||
				post.Token != "token-1" || len(post.Data) != 1 || post.Data[0]["answer"] != "aB12" {
				t.Fatal("expected the typed CAPTCHA answer for this challenge")
			}
			answered = true
			headers.Set("Set-Cookie", "captcha-test=pass; Domain=firefox.com; Path=/; Secure")
			body = `{"status":"success"}`
		default:
			t.Fatalf("unexpected request %s", req.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: headers, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	ctx := WithCaptchaPrompt(context.Background(), func(_ context.Context, shown string) (string, error) {
		if shown != image {
			t.Fatal("expected the original CAPTCHA image")
		}
		prompted = true
		return "aB12", nil
	})
	step, err := LoginContext(ctx, "captcha-test@example.invalid", "test-password", "")
	if err != nil || !step.Verified || !prompted || !answered {
		t.Fatalf("login did not resume after the CAPTCHA: %v", err)
	}
}

type loginRoundTripper func(*http.Request) (*http.Response, error)

func TestGuardianRetriesRegionalRefusalOnSameClient(t *testing.T) {
	client := Client()
	original := client.Transport
	defer func() { client.Transport = original }()
	var paths []string
	client.Transport = loginRoundTripper(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		if req.URL.Host != "vpn.mozilla.org" || req.Header.Get("Authorization") != "Bearer test-token" {
			t.Fatal("Guardian retry changed the destination or authorization")
		}
		if len(paths) == 1 {
			return &http.Response{StatusCode: http.StatusUnavailableForLegalReasons, Body: io.NopCloser(strings.NewReader(`{"error":"unavailable_in_region"}`))}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	})
	resp, _, err := guardianDo(http.MethodGet, "/api/v1/fpn/token", "test-token")
	if err != nil || resp.StatusCode != http.StatusOK ||
		strings.Join(paths, ",") != "/api/v1/fpn/token,//api/v1/fpn/token" {
		t.Fatalf("unexpected Guardian retry: paths=%v err=%v", paths, err)
	}
}

func (f loginRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestPasswordLoginRequestsEmailCodeAndExchangesVerifiedSession(t *testing.T) {
	const email = "test@example.invalid"
	session := strings.Repeat("ab", 32)
	rawSession, _ := hex.DecodeString(session)
	keyID := hex.EncodeToString(hkdfSHA256(rawSession, protocolVersion+"sessionToken", 64)[:32])
	client := Client()
	original := client.Transport
	defer func() { client.Transport = original }()
	client.Transport = loginRoundTripper(func(req *http.Request) (*http.Response, error) {
		header := req.Header.Get("Authorization")
		signedSession := strings.HasPrefix(header, `Hawk id="`+keyID+`"`) &&
			strings.Contains(header, `mac="`) && strings.Contains(header, `hash="`)
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		result := "{}"
		switch req.URL.Path {
		case "/v1/account/login":
			if body["email"] != email || body["authPW"] != deriveAuthPW(email, "test-password") ||
				body["verificationMethod"] != "email-otp" || body["sendSigninVerificationEmail"] != true {
				t.Fatal("login did not request email OTP with the derived password")
			}
			result = `{"sessionToken":"` + session + `","verified":false,"verificationMethod":"email-otp"}`
		case "/v1/session/verify_code":
			if !signedSession || body["code"] != "123456" {
				t.Fatal("email verification did not use the returned session and code")
			}
		case "/v1/oauth/token":
			if !signedSession || body["client_id"] != firefoxClientID ||
				body["grant_type"] != "fxa-credentials" || body["scope"] != oauthScope {
				t.Fatal("token request did not use the verified session and VPN scope")
			}
			result = `{"access_token":"test-access","refresh_token":"test-refresh"}`
		default:
			t.Fatalf("unexpected endpoint %s", req.URL.Path)
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(result))}, nil
	})

	step, err := Login(email, "test-password", "")
	if err != nil || step.Verified || step.VerificationMethod != "email-otp" {
		t.Fatalf("expected an email challenge: %v", err)
	}
	if err := VerifySession(step.SessionToken, "123456"); err != nil {
		t.Fatal(err)
	}
	tokens, err := OAuthToken(step.SessionToken)
	if err != nil || tokens.AccessToken != "test-access" || tokens.RefreshToken != "test-refresh" {
		t.Fatalf("expected VPN tokens: %v", err)
	}
}
