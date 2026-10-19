package mozilla

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ProxyPass struct {
	Token string
	// Expires is the pass expiry corrected for any server clock offset.
	Expires    time.Time
	QuotaLimit int64
	QuotaLeft  int64
	QuotaReset string
}

type Entitlement struct {
	Subscribed       bool   `json:"subscribed"`
	MaxBytes         string `json:"maxBytes"`
	LimitedBandwidth bool   `json:"limited_bandwidth"`
}

func guardianDo(method, path, accessToken string) (*http.Response, []byte, error) {
	c := Client()
	do := func() (*http.Response, []byte, error) {
		req, _ := http.NewRequest(method, GuardianEndpoint+path, nil)
		applyHeaders(req.Header)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		resp, err := c.Do(req)
		if err != nil {
			return nil, nil, fmt.Errorf("could not reach Mozilla VPN: %w", err)
		}
		defer resp.Body.Close()
		return resp, readBody(resp, 1<<20), nil
	}
	resp, body, err := do()
	if err == nil && resp.StatusCode == 406 {
		if serr := solveFastlyChallenge(c); serr == nil {
			resp, body, err = do()
		}
	}
	return resp, body, err
}

func FetchProxyPass(accessToken string) (*ProxyPass, error) {
	resp, body, err := guardianDo("GET", "/api/v1/fpn/token", accessToken)
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, ErrTokenInvalid
	case resp.StatusCode == 429:
		return nil, ErrQuotaExceeded
	case resp.StatusCode != 200:
		return nil, &HTTPError{resp.StatusCode, fmt.Sprintf("failed to fetch proxy pass: HTTP %d: %s", resp.StatusCode, errorSnippet(body))}
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Token == "" {
		return nil, fmt.Errorf("proxy pass response did not contain a token")
	}
	exp, nbf, err := jwtTimes(out.Token)
	if err != nil {
		return nil, err
	}
	offset := detectClockOffset(nbf, exp, time.Now())
	pass := &ProxyPass{Token: out.Token, Expires: exp.Add(offset), QuotaReset: resp.Header.Get("X-Quota-Reset")}
	pass.QuotaLimit, _ = strconv.ParseInt(resp.Header.Get("X-Quota-Limit"), 10, 64)
	pass.QuotaLeft, _ = strconv.ParseInt(resp.Header.Get("X-Quota-Remaining"), 10, 64)
	return pass, nil
}

func FetchEntitlement(accessToken string) (*Entitlement, error) {
	resp, body, err := guardianDo("GET", "/api/v1/fpn/status", accessToken)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, &HTTPError{resp.StatusCode, fmt.Sprintf("account status: HTTP %d", resp.StatusCode)}
	}
	var e Entitlement
	return &e, json.Unmarshal(body, &e)
}

// ActivateGuardian enables the VPN entitlement for the account (idempotent).
func ActivateGuardian(accessToken string) error {
	resp, body, err := guardianDo("POST", "/api/v1/fpn/activate", accessToken)
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return &HTTPError{resp.StatusCode, fmt.Sprintf("activation failed: HTTP %d: %s", resp.StatusCode, errorSnippet(body))}
	}
	return nil
}

func jwtTimes(token string) (exp, nbf time.Time, err error) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return exp, nbf, fmt.Errorf("malformed proxy pass token")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return exp, nbf, fmt.Errorf("could not decode proxy pass claims: %w", err)
	}
	var c struct {
		Exp int64 `json:"exp"`
		Nbf int64 `json:"nbf"`
		Iat int64 `json:"iat"`
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return exp, nbf, err
	}
	if c.Nbf == 0 {
		c.Nbf = c.Iat
	}
	return time.Unix(c.Exp, 0), time.Unix(c.Nbf, 0), nil
}

// detectClockOffset compensates for Guardian deployments that issue JWTs
// shifted by a fixed timezone offset: it finds a shift that makes the
// validity window contain "now", preferring the local UTC offset.
func detectClockOffset(start, exp, now time.Time) time.Duration {
	const tol = time.Minute
	plausible := func(off time.Duration) bool {
		e := exp.Add(off)
		if start.Unix() > 0 {
			s := start.Add(off)
			return !s.After(e) && !now.Before(s.Add(-tol)) && !now.After(e.Add(tol))
		}
		return !e.Before(now.Add(-tol)) && e.Sub(now) <= 2*time.Hour
	}
	if exp.Unix() == 0 || plausible(0) {
		return 0
	}
	_, localOff := now.Zone()
	candidates := []time.Duration{time.Duration(localOff) * time.Second}
	for m := -12 * 60; m <= 14*60; m += 15 {
		candidates = append(candidates, time.Duration(m)*time.Minute)
	}
	for _, off := range candidates {
		if off != 0 && plausible(off) {
			return off
		}
	}
	return 0
}
