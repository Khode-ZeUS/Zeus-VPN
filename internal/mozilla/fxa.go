package mozilla

import (
	"bytes"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	fxaAuthServer        = "https://api.accounts.firefox.com/v1"
	fxaProfileURL        = "https://profile.accounts.firefox.com/v1/profile"
	firefoxClientID      = "5882386c6d801776"
	oauthScope           = "profile https://identity.mozilla.com/apps/vpn"
	protocolVersion      = "identity.mozilla.com/picl/v1/"
	maxChallengeAttempts = 5

	ErrnoAccountUnknown    = 102
	ErrnoIncorrectPassword = 103
	ErrnoInvalidParameter  = 107
	ErrnoRequestBlocked    = 125
	ErrnoInvalidUnblock    = 127
	ErrnoInvalidVerifyCode = 183
)

// FxaError is an error payload returned by Firefox Accounts.
type FxaError struct {
	Message string
	Errno   int
	Status  int
}

func (e *FxaError) Error() string {
	switch e.Errno {
	case ErrnoAccountUnknown:
		return "No Firefox account exists for this email."
	case ErrnoIncorrectPassword:
		return "Incorrect password."
	case ErrnoInvalidUnblock:
		return "Invalid unblock code."
	case ErrnoInvalidVerifyCode:
		return "Invalid or expired verification code."
	}
	return e.Message
}

type LoginResponse struct {
	UID                string
	SessionToken       string
	Verified           bool
	VerificationMethod string
}

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Scope        string `json:"scope"`
}

func hkdfSHA256(secret []byte, info string, n int) []byte {
	out, err := hkdf.Key(sha256.New, secret, make([]byte, 32), info, n)
	if err != nil {
		panic(err)
	}
	return out
}

func deriveAuthPW(email, password string) string {
	quick, err := pbkdf2.Key(sha256.New, password, []byte(protocolVersion+"quickStretch:"+email), 1000, 32)
	if err != nil {
		panic(err)
	}
	return hex.EncodeToString(hkdfSHA256(quick, protocolVersion+"authPW", 32))
}

func hawkHeader(method string, u *url.URL, sessionTokenHex string, body []byte) (string, error) {
	raw, err := hex.DecodeString(sessionTokenHex)
	if err != nil {
		return "", fmt.Errorf("malformed session token: %w", err)
	}
	expanded := hkdfSHA256(raw, protocolVersion+"sessionToken", 64)
	tokenID, key := hex.EncodeToString(expanded[:32]), expanded[32:]

	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nb := make([]byte, 6)
	rand.Read(nb)
	nonce := base64.RawURLEncoding.EncodeToString(nb)
	port := u.Port()
	if port == "" {
		port = "443"
	}
	var payloadHash string
	if len(body) > 0 {
		h := sha256.New()
		h.Write([]byte("hawk.1.payload\napplication/json\n"))
		h.Write(body)
		h.Write([]byte("\n"))
		payloadHash = base64.StdEncoding.EncodeToString(h.Sum(nil))
	}
	normalized := strings.Join([]string{"hawk.1.header", ts, nonce, strings.ToUpper(method), u.RequestURI(), u.Hostname(), port, payloadHash, "", ""}, "\n")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(normalized))
	header := fmt.Sprintf(`Hawk id="%s", ts="%s", nonce="%s", mac="%s"`, tokenID, ts, nonce, base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	if payloadHash != "" {
		header += fmt.Sprintf(`, hash="%s"`, payloadHash)
	}
	return header, nil
}

// fxaDo sends an FxA request, solving Fastly's challenge on HTTP 406. The
// Hawk header is rebuilt on every attempt so its timestamp stays fresh.
func fxaDo(method, path, sessionToken string, payload any) (map[string]any, error) {
	c := Client()
	u := mustURL(fxaAuthServer + path)
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	var resp *http.Response
	var respBody []byte
	for attempt := 0; attempt < maxChallengeAttempts; attempt++ {
		req, _ := http.NewRequest(method, u.String(), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		applyHeaders(req.Header)
		if sessionToken != "" {
			h, err := hawkHeader(method, u, sessionToken, body)
			if err != nil {
				return nil, err
			}
			req.Header.Set("Authorization", h)
		}
		var err error
		resp, err = c.Do(req)
		if err != nil {
			return nil, fmt.Errorf("could not reach Firefox Accounts: %w", err)
		}
		respBody = readBody(resp, 1<<20)
		resp.Body.Close()
		if resp.StatusCode != 406 {
			break
		}
		if err := solveFastlyChallenge(c); err != nil {
			return nil, fmt.Errorf("Mozilla anti-bot check failed: %w", err)
		}
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Errno   int    `json:"errno"`
			Message string `json:"message"`
		}
		json.Unmarshal(respBody, &e)
		msg := e.Message
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, errorSnippet(respBody))
		}
		return nil, &FxaError{Message: msg, Errno: e.Errno, Status: resp.StatusCode}
	}
	out := map[string]any{}
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("bad FxA response: %w", err)
		}
	}
	return out, nil
}

func asString(m map[string]any, k string) string { s, _ := m[k].(string); return s }

// Login signs in with the onepw protocol. unblockCode is only needed after
// FxA rejected a previous attempt with ErrnoRequestBlocked.
func Login(email, password, unblockCode string) (*LoginResponse, error) {
	body := map[string]any{"email": email, "authPW": deriveAuthPW(email, password), "verificationMethod": "email-2fa"}
	if unblockCode != "" {
		body["unblockCode"] = unblockCode
	}
	data, err := fxaDo("POST", "/account/login", "", body)
	if fe, ok := err.(*FxaError); ok && fe.Errno == ErrnoInvalidParameter {
		delete(body, "verificationMethod")
		data, err = fxaDo("POST", "/account/login", "", body)
	}
	if err != nil {
		return nil, err
	}
	verified, _ := data["verified"].(bool)
	return &LoginResponse{
		UID:                asString(data, "uid"),
		SessionToken:       asString(data, "sessionToken"),
		Verified:           verified,
		VerificationMethod: asString(data, "verificationMethod"),
	}, nil
}

func SendUnblockCode(email string) error {
	_, err := fxaDo("POST", "/account/login/send_unblock_code", "", map[string]any{"email": email})
	return err
}

func VerifySession(sessionToken, code string) error {
	_, err := fxaDo("POST", "/session/verify_code", sessionToken, map[string]any{"code": code})
	return err
}

func OAuthToken(sessionToken string) (*TokenResponse, error) {
	data, err := fxaDo("POST", "/oauth/token", sessionToken, map[string]any{
		"client_id": firefoxClientID, "grant_type": "fxa-credentials", "scope": oauthScope, "access_type": "offline",
	})
	if err != nil {
		return nil, err
	}
	return tokenFrom(data, ""), nil
}

func RefreshToken(refresh string) (*TokenResponse, error) {
	data, err := fxaDo("POST", "/oauth/token", "", map[string]any{
		"client_id": firefoxClientID, "grant_type": "refresh_token", "refresh_token": refresh, "scope": oauthScope,
	})
	if err != nil {
		return nil, err
	}
	return tokenFrom(data, refresh), nil
}

func DestroyToken(token string) {
	fxaDo("POST", "/oauth/destroy", "", map[string]any{"client_id": firefoxClientID, "token": token})
}

func tokenFrom(data map[string]any, fallbackRefresh string) *TokenResponse {
	t := &TokenResponse{
		AccessToken:  asString(data, "access_token"),
		RefreshToken: asString(data, "refresh_token"),
		Scope:        asString(data, "scope"),
	}
	if t.RefreshToken == "" {
		t.RefreshToken = fallbackRefresh
	}
	if f, ok := data["expires_in"].(float64); ok {
		t.ExpiresIn = int64(f)
	}
	return t
}

type Profile struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
}

func FetchProfile(accessToken string) (*Profile, error) {
	req, _ := http.NewRequest("GET", fxaProfileURL, nil)
	applyHeaders(req.Header)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := Client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body := readBody(resp, 1<<20)
	if resp.StatusCode != 200 {
		return nil, &HTTPError{Status: resp.StatusCode, Msg: fmt.Sprintf("profile: HTTP %d", resp.StatusCode)}
	}
	var p Profile
	return &p, json.Unmarshal(body, &p)
}
