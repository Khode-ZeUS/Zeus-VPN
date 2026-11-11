package mozilla

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Solver for Fastly's JavaScript "Client Challenge" that sits in front of
// some Firefox Accounts endpoints and answers HTTP 406.

const (
	fxaAPIHost          = "https://api.accounts.firefox.com"
	fxaSiteHost         = "https://accounts.firefox.com"
	maxPostBackRounds   = 8
	powAlphabet         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	fastlySolverTimeout = 60 * time.Second
)

var (
	challengeSlot           = make(chan struct{}, 1)
	challengePrefixRe       = regexp.MustCompile(`/_fs-ch-[A-Za-z0-9]+`)
	initStartRe             = regexp.MustCompile(`\binit\s*\(`)
	initCallRe              = regexp.MustCompile(`init\((\[[^\]]*\]),\s*"([^"]+)",\s*"([^"]+)"`)
	errNoChallenge          = errors.New("host did not serve a Fastly challenge page")
	ErrInteractiveChallenge = errors.New("Mozilla requires verification in the sign-in window")
	ErrVerificationRejected = errors.New("Mozilla verification returned HTTP 400")
)

func solvePoW(base, targetHex string) (string, bool) {
	target, err := hex.DecodeString(targetHex)
	if err != nil || len(target) != sha256.Size {
		return "", false
	}
	for i := 0; i < len(powAlphabet); i++ {
		for j := 0; j < len(powAlphabet); j++ {
			suffix := string([]byte{powAlphabet[i], powAlphabet[j]})
			sum := sha256.Sum256([]byte(base + suffix))
			if bytes.Equal(sum[:], target) {
				return suffix, true
			}
		}
	}
	return "", false
}

func baseOrigin(prefixURL string) string {
	if i := strings.Index(prefixURL, "/_fs-ch-"); i > 0 {
		return prefixURL[:i]
	}
	return prefixURL
}

// parseChallengeInit extracts the challenge list and its token from the
// challenge script's init(...) call. Fastly has shipped two shapes: the
// legacy one-liner and a newer multi-line call with JavaScript comments
// between the arguments — both carry JSON after the opening bracket, so the
// arguments are decoded as JSON instead of being regexed.
func parseChallengeInit(script, prefix string) ([]map[string]any, string, error) {
	// Legacy one-liner first: init([{…}], "token", "/_fs-ch-…").
	if m := initCallRe.FindStringSubmatch(script); m != nil {
		var legacy []map[string]any
		if err := json.Unmarshal([]byte(m[1]), &legacy); err == nil && len(legacy) > 0 {
			return legacy, m[2], nil
		}
	}
	// Current shape: walk every init( occurrence and decode
	// ([challenges], "token", "prefix") as JSON straight off the script,
	// keeping the last call whose prefix matches the page's asset prefix.
	starts := initStartRe.FindAllStringIndex(script, -1)
	for i := len(starts) - 1; i >= 0; i-- {
		rest := script[starts[i][1]:]
		var challenges []map[string]any
		var token, callPrefix string
		if !decodeArgs(&rest, &challenges, &token, &callPrefix) {
			continue
		}
		if len(challenges) == 0 || token == "" || strings.Contains(callPrefix, "/_fs-ch-") {
			return challenges, token, nil
		}
	}
	return nil, "", errors.New("valid challenge init call not found in script")
}

// decodeArgs JSON-decodes the arguments packed into rest ("[challenges],
// "token", "prefix""), advancing rest past each one; false when they stop
// parsing.
func decodeArgs(rest *string, targets ...any) bool {
	for i, target := range targets {
		*rest = skipWSComments(*rest)
		if i > 0 {
			if !strings.HasPrefix(*rest, ",") {
				return false
			}
			*rest = skipWSComments((*rest)[1:])
		}
		dec := json.NewDecoder(strings.NewReader(*rest))
		if err := dec.Decode(target); err != nil {
			return false
		}
		*rest = (*rest)[dec.InputOffset():]
	}
	return true
}

// skipWSComments strips whitespace and JavaScript comments from the front
// of s (the challenge script sprinkles them around the init arguments).
func skipWSComments(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "//"):
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[i+1:]
				continue
			}
			return ""
		case strings.HasPrefix(s, "/*"):
			if i := strings.Index(s, "*/"); i >= 0 {
				s = s[i+2:]
				continue
			}
			return ""
		}
		return s
	}
}

func fetchText(ctx context.Context, c *http.Client, u string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	// The challenge assets are fetched with the SAME user agent the
	// API calls use: the anti-bot cookie Fastly issues is bound to the
	// client fingerprint, and a cookie earned as one client is refused
	// for another.
	req.Header.Set("User-Agent", firefoxUserAgent())
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body := readBody(resp, 4<<20)
	if resp.StatusCode == http.StatusBadRequest {
		return "", fmt.Errorf("GET %s: %w", u, ErrVerificationRejected)
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GET %s returned HTTP %d", u, resp.StatusCode)
	}
	return string(body), nil
}

func fetchChallengePage(ctx context.Context, c *http.Client, pageURL string) (string, bool, error) {
	readPage := func() (string, error) {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return fetchText(attempt, c, pageURL)
	}
	body, err := readPage()
	var timeout net.Error
	if ctx.Err() == nil && errors.As(err, &timeout) && timeout.Timeout() {
		body, err = readPage()
	}
	if err != nil {
		return "", false, err
	}
	return body, strings.Contains(body, "/_fs-ch-") && strings.Contains(body, "Client Challenge"), nil
}

func postJSON(ctx context.Context, c *http.Client, u, origin, accept string, payload any) (*http.Response, []byte, error) {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	// The challenge assets are fetched with the SAME user agent the
	// API calls use: the anti-bot cookie Fastly issues is bound to the
	// client fingerprint, and a cookie earned as one client is refused
	// for another.
	req.Header.Set("User-Agent", firefoxUserAgent())
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Origin", origin)
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	return resp, readBody(resp, 1<<20), nil
}

func fetchPAT(ctx context.Context, c *http.Client, prefixURL, token string) (string, error) {
	resp, body, err := postJSON(ctx, c, prefixURL+"/pat?token="+url.QueryEscape(token), baseOrigin(prefixURL), "text/plain", nil)
	if err != nil {
		return "", err
	}
	// The client cannot mint a Private Access Token; an empty answer makes
	// Fastly fall back to a solvable challenge.
	if resp.StatusCode == 400 || resp.StatusCode == 401 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("PAT request returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Auth string `json:"auth"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Auth == "" {
		return "", errors.New("empty PAT auth token")
	}
	return out.Auth, nil
}

type captchaPromptKey struct{}

// WithCaptchaPrompt lets an interactive sign-in display the original image
// and wait for the user's answer. Background requests never prompt.
func WithCaptchaPrompt(ctx context.Context, prompt func(context.Context, string) (string, error)) context.Context {
	ctx = context.WithValue(ctx, signInClientKey{}, &signInClientState{})
	return context.WithValue(ctx, captchaPromptKey{}, prompt)
}

func answerChallenge(ctx context.Context, c *http.Client, prefixURL, token string, ch map[string]any) (map[string]any, error) {
	data, _ := ch["data"].(map[string]any)
	str := func(k string) string { s, _ := data[k].(string); return s }
	switch ch["ty"] {
	case "pow":
		base := str("base")
		answer, ok := solvePoW(base, str("hash"))
		if !ok {
			return nil, fmt.Errorf("no proof-of-work solution for base %q", base)
		}
		return map[string]any{"ty": "pow", "base": base, "answer": answer, "hmac": str("hmac"), "expires": str("expires")}, nil
	case "pat":
		auth, err := fetchPAT(ctx, c, prefixURL, token)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ty": "pat", "auth": auth}, nil
	case "clientmetrics":
		return map[string]any{
			"ty":                   "clientmetrics",
			"webdriver":            false,
			"bot_detection_result": map[string]any{"bot_detected": false, "bot_kind": nil},
			"browser_metrics":      map[string]any{"client_data": "{}", "error_trace": nil},
			"detector_results":     map[string]any{},
			"v":                    2,
		}, nil
	case "captcha":
		prompt, _ := ctx.Value(captchaPromptKey{}).(func(context.Context, string) (string, error))
		if prompt == nil {
			return nil, ErrInteractiveChallenge
		}
		kind, encoded, ok := strings.Cut(str("image_b64"), ",")
		switch kind {
		case "data:image/png;base64", "data:image/jpeg;base64", "data:image/gif;base64", "data:image/webp;base64":
		default:
			return nil, errors.New("unsupported CAPTCHA image format")
		}
		if !ok || len(encoded) == 0 || len(encoded) > 512<<10 {
			return nil, errors.New("invalid CAPTCHA image size")
		}
		if _, err := base64.StdEncoding.DecodeString(encoded); err != nil {
			return nil, errors.New("invalid CAPTCHA image encoding")
		}
		answer, err := prompt(ctx, kind+","+encoded)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ty": "captcha", "answer": strings.TrimSpace(answer)}, nil
	}
	return nil, fmt.Errorf("unsupported Fastly challenge %v", ch["ty"])
}

func solveOnHost(ctx context.Context, main *http.Client, base string) error {
	// A fresh cookie jar (a client already holding a valid cookie gets the
	// real page instead of the challenge) sharing the main transport, so the
	// challenge is solved through the same exit as the retried request.
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	solver := &http.Client{Transport: main.Transport, Jar: jar, Timeout: fastlySolverTimeout}
	pageURL := base + "/"
	page, isChallenge, err := fetchChallengePage(ctx, solver, pageURL)
	if err != nil {
		return err
	}
	if !isChallenge {
		return errNoChallenge
	}
	prefix := challengePrefixRe.FindString(page)
	if prefix == "" {
		return fmt.Errorf("challenge asset prefix not found on %s", base)
	}
	prefixURL := base + prefix
	scriptURL := prefixURL + "/script.js?reload=true"
	readScript := func() (string, error) {
		attempt, cancel := context.WithTimeout(ctx, 12*time.Second)
		defer cancel()
		return fetchText(attempt, solver, scriptURL)
	}
	script, err := readScript()
	var networkError net.Error
	if ctx.Err() == nil && errors.As(err, &networkError) {
		ResetConnections()
		script, err = readScript()
	}
	if err != nil {
		return err
	}
	challenges, token, perr := parseChallengeInit(script, prefix)
	if perr != nil {
		return perr
	}

	for round := 0; round < maxPostBackRounds; round++ {
		answers := make([]map[string]any, 0, len(challenges))
		for _, ch := range challenges {
			a, err := answerChallenge(ctx, solver, prefixURL, token, ch)
			if err != nil {
				return err
			}
			answers = append(answers, a)
		}
		resp, body, err := postJSON(ctx, solver, prefixURL+"/fst-post-back", baseOrigin(prefixURL), "application/json",
			map[string]any{"token": token, "data": answers})
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusBadRequest {
			return ErrVerificationRejected
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("challenge post-back returned HTTP %d", resp.StatusCode)
		}
		var out struct {
			Status string           `json:"status"`
			Ch     []map[string]any `json:"ch"`
			Tok    string           `json:"tok"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return fmt.Errorf("bad post-back response: %v", err)
		}
		if out.Status == "success" {
			if _, still, err := fetchChallengePage(ctx, solver, pageURL); err == nil && still {
				return errors.New("challenge cookie not accepted on this exit IP")
			}
			cookies := jar.Cookies(mustURL(pageURL))
			if len(cookies) == 0 {
				return errors.New("challenge completed but no cookies were issued")
			}
			for _, host := range []string{fxaAPIHost, fxaSiteHost} {
				u := mustURL(host + "/")
				var cs []*http.Cookie
				for _, c := range cookies {
					cs = append(cs, &http.Cookie{Name: c.Name, Value: c.Value, Domain: "firefox.com", Path: "/"})
				}
				main.Jar.SetCookies(u, cs)
			}
			return nil
		}
		if len(out.Ch) == 0 || out.Tok == "" {
			return fmt.Errorf("unexpected post-back response: %s", errorSnippet(body))
		}
		challenges, token = out.Ch, out.Tok
	}
	return fmt.Errorf("Fastly challenge did not complete within %d rounds", maxPostBackRounds)
}

func solveFastlyChallenge(ctx context.Context, main *http.Client) error {
	select {
	case challengeSlot <- struct{}{}:
		defer func() { <-challengeSlot }()
	case <-ctx.Done():
		return ctx.Err()
	}
	var lastErr error = errNoChallenge
	for _, base := range []string{fxaAPIHost, fxaSiteHost} {
		err := solveOnHost(ctx, main, base)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrInteractiveChallenge) {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !errors.Is(err, errNoChallenge) {
			lastErr = err
		}
	}
	return lastErr
}
