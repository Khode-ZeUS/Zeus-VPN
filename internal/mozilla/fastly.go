package mozilla

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/publicsuffix"
)

// Solver for Fastly's JavaScript "Client Challenge" that sits in front of
// some Firefox Accounts endpoints and answers HTTP 406.

const (
	fxaAPIHost          = "https://api.accounts.firefox.com"
	fxaSiteHost         = "https://accounts.firefox.com"
	fastlySolverUA      = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
	maxPostBackRounds   = 8
	powAlphabet         = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	fastlySolverTimeout = 60 * time.Second
)

var (
	challengePrefixRe = regexp.MustCompile(`/_fs-ch-[A-Za-z0-9]+`)
	initCallRe        = regexp.MustCompile(`init\((\[[^\]]*\]),\s*"([^"]+)",\s*"([^"]+)"`)
	solveMu           sync.Mutex
	errNoChallenge    = errors.New("host did not serve a Fastly challenge page")
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

func fetchText(c *http.Client, u string) (string, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", fastlySolverUA)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body := readBody(resp, 4<<20)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("GET %s returned HTTP %d", u, resp.StatusCode)
	}
	return string(body), nil
}

func fetchChallengePage(c *http.Client, pageURL string) (string, bool, error) {
	body, err := fetchText(c, pageURL)
	if err != nil {
		return "", false, err
	}
	return body, strings.Contains(body, "/_fs-ch-") && strings.Contains(body, "Client Challenge"), nil
}

func postJSON(c *http.Client, u, origin, accept string, payload any) (*http.Response, []byte, error) {
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req, _ := http.NewRequest("POST", u, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", fastlySolverUA)
	req.Header.Set("Origin", origin)
	resp, err := c.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	return resp, readBody(resp, 1<<20), nil
}

func fetchPAT(c *http.Client, prefixURL, token string) (string, error) {
	resp, body, err := postJSON(c, prefixURL+"/pat?token="+url.QueryEscape(token), baseOrigin(prefixURL), "text/plain", nil)
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

func answerChallenge(c *http.Client, prefixURL, token string, ch map[string]any) (map[string]any, error) {
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
		auth, err := fetchPAT(c, prefixURL, token)
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
	}
	return nil, fmt.Errorf("unsupported Fastly challenge %v (captcha cannot be solved automatically)", ch["ty"])
}

func solveOnHost(main *http.Client, base string) error {
	// A fresh cookie jar (a client already holding a valid cookie gets the
	// real page instead of the challenge) sharing the main transport, so the
	// challenge is solved through the same exit as the retried request.
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	solver := &http.Client{Transport: main.Transport, Jar: jar, Timeout: fastlySolverTimeout}
	pageURL := base + "/"
	page, isChallenge, err := fetchChallengePage(solver, pageURL)
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
	script, err := fetchText(solver, prefixURL+"/script.js?reload=true")
	if err != nil {
		return err
	}
	matches := initCallRe.FindAllStringSubmatch(script, -1)
	if len(matches) == 0 {
		return errors.New("challenge init() call not found in script")
	}
	last := matches[len(matches)-1]
	var challenges []map[string]any
	if err := json.Unmarshal([]byte(last[1]), &challenges); err != nil || len(challenges) == 0 {
		return errors.New("could not parse challenge list")
	}
	token := last[2]

	for round := 0; round < maxPostBackRounds; round++ {
		answers := make([]map[string]any, 0, len(challenges))
		for _, ch := range challenges {
			a, err := answerChallenge(solver, prefixURL, token, ch)
			if err != nil {
				return err
			}
			answers = append(answers, a)
		}
		resp, body, err := postJSON(solver, prefixURL+"/fst-post-back", baseOrigin(prefixURL), "application/json",
			map[string]any{"token": token, "data": answers})
		if err != nil {
			return err
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
			if _, still, err := fetchChallengePage(solver, pageURL); err == nil && still {
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

func solveFastlyChallenge(main *http.Client) error {
	solveMu.Lock()
	defer solveMu.Unlock()
	var lastErr error = errNoChallenge
	for _, base := range []string{fxaAPIHost, fxaSiteHost} {
		err := solveOnHost(main, base)
		if err == nil {
			return nil
		}
		if !errors.Is(err, errNoChallenge) {
			lastErr = err
		}
	}
	return lastErr
}
