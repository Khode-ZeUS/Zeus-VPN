package core

import (
	"context"
	"errors"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/store"
)

var (
	ErrNotLoggedIn    = errors.New("not signed in")
	ErrSessionExpired = errors.New("the sign-in for this account expired, please sign in again")
)

// LoginStep tells the UI what the sign-in flow needs next.
type LoginStep struct {
	Step    string `json:"step"` // "done", "code", "link", "unblock", "verified"
	Message string `json:"message"`
}

// AccountInfo is what the UI shows for one account.
type AccountInfo struct {
	Email      string `json:"email"`
	Active     bool   `json:"active"`
	QuotaLimit int64  `json:"quotaLimit"`
	QuotaLeft  int64  `json:"quotaLeft"`
	QuotaReset int64  `json:"quotaReset"` // unix ms, 0 if unknown
	Exhausted  bool   `json:"exhausted"`
	NeedsLogin bool   `json:"needsLogin"`
}

// Auth manages any number of Mozilla accounts, one of them active.
type Auth struct {
	mu   sync.Mutex
	accs *store.Accounts

	pendingEmail    string
	pendingPassword string
	pendingSession  string
	pendingVerified bool
	pendingTOTP     bool
}

func NewAuth() *Auth { return &Auth{accs: store.LoadAccounts()} }

// Reload re-reads the accounts from disk, for when another process (the iOS
// network extension) may have refreshed tokens or quotas.
func (a *Auth) Reload() {
	accs := store.LoadAccounts()
	a.mu.Lock()
	a.accs = accs
	a.mu.Unlock()
}

var invisible = regexp.MustCompile(`[\s\x{200B}-\x{200D}\x{FEFF}\x{00A0}]+`)

func cleanEmail(s string) string { return invisible.ReplaceAllString(s, "") }

func (a *Auth) saveLocked() { store.SaveAccounts(a.accs) }

func (a *Auth) LoggedIn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.accs.List) > 0
}

func (a *Auth) ActiveEmail() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.accs.Active
}

func (a *Auth) Accounts() []AccountInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AccountInfo, 0, len(a.accs.List))
	for _, acc := range a.accs.List {
		info := AccountInfo{
			Email:      acc.Email,
			Active:     acc.Email == a.accs.Active,
			QuotaLimit: acc.QuotaLimit,
			QuotaLeft:  max(0, acc.QuotaLeft-acc.QuotaLocalUsed),
			Exhausted:  acc.Exhausted && !acc.Usable(),
			NeedsLogin: acc.NeedsLogin,
		}
		if !acc.QuotaReset.IsZero() {
			info.QuotaReset = acc.QuotaReset.UnixMilli()
		}
		out = append(out, info)
	}
	return out
}

func (a *Auth) SetActive(email string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.accs.Find(email) == nil {
		return errors.New("unknown account")
	}
	a.accs.Active = email
	a.saveLocked()
	return nil
}

// Remove signs one account out. If it was active, the best remaining
// account becomes active.
func (a *Auth) Remove(email string) {
	a.mu.Lock()
	var refresh string
	for i, acc := range a.accs.List {
		if acc.Email == email {
			refresh = acc.Tokens.RefreshToken
			a.accs.List = append(a.accs.List[:i], a.accs.List[i+1:]...)
			break
		}
	}
	if a.accs.Active == email {
		a.accs.Active = a.bestLocked()
	}
	a.saveLocked()
	a.mu.Unlock()
	if refresh != "" {
		go mozilla.DestroyToken(refresh)
	}
}

// SetEmail fills in the address of an account migrated without one.
func (a *Auth) SetEmail(old, email string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.accs.Find(old)
	if acc == nil || email == "" || (email != old && a.accs.Find(email) != nil) {
		return
	}
	acc.Email = email
	if a.accs.Active == old {
		a.accs.Active = email
	}
	a.saveLocked()
}

// AddWebAccount stores an account signed in through the web login flow
// (the FxA page hands the session over; the API password-login route is
// edge-blocked). The email comes from the page or the FxA profile.
func (a *Auth) AddWebAccount(email string, t *mozilla.TokenResponse) error {
	email = cleanEmail(email)
	if email == "" {
		return errors.New("the sign-in page did not reveal the account's email")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.accs.Find(email)
	if acc == nil {
		a.accs.List = append(a.accs.List, store.Account{Email: email})
		acc = a.accs.Find(email)
	}
	acc.Tokens = tokensFrom(t)
	acc.NeedsLogin = false
	a.accs.Active = email
	a.saveLocked()
	return nil
}

// AddWebAccountTokens stores an account whose OAuth tokens were already
// exchanged (inside the web sign-in page), so no network call is needed.
func (a *Auth) AddWebAccountTokens(email string, tokens store.Tokens) error {
	email = cleanEmail(email)
	if email == "" {
		return errors.New("the sign-in page did not reveal the account's email")
	}
	if tokens.AccessToken == "" {
		return errors.New("the sign-in page did not return an access token")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.accs.Find(email)
	if acc == nil {
		a.accs.List = append(a.accs.List, store.Account{Email: email})
		acc = a.accs.Find(email)
	}
	acc.Tokens = tokens
	acc.NeedsLogin = false
	a.accs.Active = email
	a.saveLocked()
	return nil
}

// AccessToken returns a valid OAuth access token for the account ("" =
// active one), refreshing it when expired or when force is set.
func (a *Auth) AccessToken(email string, force bool) (string, error) {
	a.mu.Lock()
	if email == "" {
		email = a.accs.Active
	}
	acc := a.accs.Find(email)
	if acc == nil {
		a.mu.Unlock()
		return "", ErrNotLoggedIn
	}
	if !force && acc.Tokens.Valid() {
		tok := acc.Tokens.AccessToken
		a.mu.Unlock()
		return tok, nil
	}
	refresh := acc.Tokens.RefreshToken
	a.mu.Unlock()
	if refresh == "" {
		a.markNeedsLogin(email)
		return "", ErrSessionExpired
	}
	t, err := mozilla.RefreshToken(refresh)
	if err != nil {
		var fe *mozilla.FxaError
		if errors.As(err, &fe) && fe.Status >= 400 && fe.Status < 500 {
			a.markNeedsLogin(email)
			return "", ErrSessionExpired
		}
		return "", err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if acc := a.accs.Find(email); acc != nil {
		acc.Tokens = tokensFrom(t)
		acc.NeedsLogin = false
		a.saveLocked()
	}
	return t.AccessToken, nil
}

func tokensFrom(t *mozilla.TokenResponse) store.Tokens {
	return store.Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Scope: t.Scope, ObtainedAt: time.Now(), ExpiresIn: t.ExpiresIn}
}

func (a *Auth) markNeedsLogin(email string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if acc := a.accs.Find(email); acc != nil {
		acc.NeedsLogin = true
		a.saveLocked()
	}
}

// RecordQuota stores the quota reported with a proxy pass.
func (a *Auth) RecordQuota(email string, p *mozilla.ProxyPass) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.accs.Find(email)
	if acc == nil || p.QuotaLimit <= 0 {
		return
	}
	if acc.QuotaLimit == p.QuotaLimit && acc.QuotaStamp == p.QuotaReset && p.QuotaLeft <= acc.QuotaLeft {
		acc.QuotaLocalUsed = max(0, acc.QuotaLocalUsed-(acc.QuotaLeft-p.QuotaLeft))
	} else {
		acc.QuotaLocalUsed = 0
	}
	acc.QuotaLimit, acc.QuotaLeft, acc.QuotaAt = p.QuotaLimit, p.QuotaLeft, time.Now()
	acc.QuotaStamp = p.QuotaReset
	if t, err := time.Parse(time.RFC3339, p.QuotaReset); err == nil {
		acc.QuotaReset = t
	}
	acc.Exhausted = p.QuotaLeft <= 0
	a.saveLocked()
}

func (a *Auth) cachedQuota(email string) (mozilla.ProxyPass, int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.accs.Find(email)
	if acc == nil || (!acc.QuotaReset.IsZero() && !time.Now().Before(acc.QuotaReset)) {
		return mozilla.ProxyPass{}, 0
	}
	return mozilla.ProxyPass{QuotaLimit: acc.QuotaLimit, QuotaLeft: acc.QuotaLeft, QuotaReset: acc.QuotaStamp}, acc.QuotaLocalUsed
}

func (a *Auth) saveLocalQuota(q quotaEstimate, bytes int64) {
	if q.limit <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	acc := a.accs.Find(q.account)
	if acc == nil || acc.QuotaLimit != q.limit || acc.QuotaLeft != q.left || acc.QuotaStamp != q.reset {
		return
	}
	if used := q.left - q.remaining(bytes); used > acc.QuotaLocalUsed {
		acc.QuotaLocalUsed = used
		a.saveLocked()
	}
}

// MarkExhausted records that Mozilla refused the account for lack of data.
func (a *Auth) MarkExhausted(email string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if acc := a.accs.Find(email); acc != nil {
		acc.Exhausted = true
		acc.QuotaLeft = 0
		acc.QuotaLocalUsed = 0
		if acc.QuotaReset.IsZero() || time.Now().After(acc.QuotaReset) {
			now := time.Now().UTC()
			acc.QuotaReset = time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		}
		a.saveLocked()
	}
}

// NextUsable picks the usable account with the most data left, skipping
// the ones in tried.
func (a *Auth) NextUsable(tried map[string]bool) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pickLocked(tried, false)
}

// bestLocked picks the best account, or any account if none is usable.
func (a *Auth) bestLocked() string {
	return a.pickLocked(nil, true)
}

func (a *Auth) pickLocked(tried map[string]bool, fallbackAny bool) string {
	type cand struct {
		email string
		left  int64
	}
	var cands []cand
	for i := range a.accs.List {
		acc := &a.accs.List[i]
		if tried[acc.Email] || !acc.Usable() {
			continue
		}
		left := acc.QuotaLeft
		if acc.QuotaLimit == 0 || (!acc.QuotaReset.IsZero() && time.Now().After(acc.QuotaReset)) {
			left = 1 << 62 // unknown or already reset: assume plenty
		}
		cands = append(cands, cand{acc.Email, left})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].left > cands[j].left })
	if len(cands) == 0 {
		if fallbackAny && len(a.accs.List) > 0 {
			return a.accs.List[0].Email
		}
		return ""
	}
	return cands[0].email
}

// --- sign-in flow (adds or refreshes an account) ---

func (a *Auth) finish(ctx context.Context, session string) (*LoginStep, error) {
	t, err := mozilla.OAuthTokenContext(ctx, session)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session != a.pendingSession {
		return nil, errors.New("sign-in expired, please start again")
	}
	email := a.pendingEmail
	acc := a.accs.Find(email)
	if acc == nil {
		// A migrated account without a known email may be this same login.
		if blank := a.accs.Find(""); blank != nil {
			acc = blank
			acc.Email = email
			if a.accs.Active == "" {
				a.accs.Active = email
			}
		} else {
			a.accs.List = append(a.accs.List, store.Account{Email: email})
			acc = &a.accs.List[len(a.accs.List)-1]
		}
	}
	acc.Tokens = tokensFrom(t)
	acc.NeedsLogin = false
	if a.accs.Active == "" || a.accs.Find(a.accs.Active) == nil {
		a.accs.Active = email
	}
	a.saveLocked()
	a.pendingEmail, a.pendingPassword, a.pendingSession = "", "", ""
	a.pendingVerified = false
	a.pendingTOTP = false
	return &LoginStep{Step: "done"}, nil
}

func (a *Auth) Login(ctx context.Context, email, password string) (*LoginStep, error) {
	email = cleanEmail(email)
	if email == "" || password == "" {
		return nil, errors.New("enter your email and password")
	}
	return a.login(ctx, email, password, "")
}

func (a *Auth) login(ctx context.Context, email, password, unblock string) (*LoginStep, error) {
	resp, err := mozilla.LoginContext(ctx, email, password, unblock)
	if err != nil {
		var fe *mozilla.FxaError
		if errors.As(err, &fe) && fe.Errno == mozilla.ErrnoRequestBlocked {
			a.mu.Lock()
			if ctx.Err() != nil {
				a.mu.Unlock()
				return nil, ctx.Err()
			}
			a.pendingEmail, a.pendingPassword = email, password
			a.pendingSession, a.pendingVerified = "", false
			a.mu.Unlock()
			if serr := mozilla.SendUnblockCode(ctx, email); serr != nil {
				return nil, serr
			}
			return &LoginStep{Step: "unblock", Message: "Mozilla blocked this sign-in for safety. Enter the unblock code sent to " + email + "."}, nil
		}
		return nil, err
	}
	a.mu.Lock()
	if ctx.Err() != nil {
		a.mu.Unlock()
		return nil, ctx.Err()
	}
	a.pendingEmail, a.pendingSession = email, resp.SessionToken
	a.pendingPassword = ""
	a.pendingVerified = resp.Verified
	a.pendingTOTP = resp.VerificationMethod == "totp-2fa"
	a.mu.Unlock()
	if !resp.Verified {
		if resp.VerificationMethod == "totp-2fa" {
			return &LoginStep{Step: "totp", Message: "Enter the code from your authenticator app."}, nil
		}
		if resp.VerificationMethod == "email" {
			return &LoginStep{Step: "link", Message: "Open the confirmation link sent to " + email + ", then continue."}, nil
		}
		return &LoginStep{Step: "code", Message: "Enter the 6-digit code sent to " + email + "."}, nil
	}
	return a.ContinueVerifiedLogin(ctx)
}

func (a *Auth) PendingEmail() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingEmail
}

func (a *Auth) VerifyCode(ctx context.Context, code string) (*LoginStep, error) {
	a.mu.Lock()
	session := a.pendingSession
	totp := a.pendingTOTP
	a.mu.Unlock()
	if session == "" {
		return nil, errors.New("sign-in expired, please start again")
	}
	verify := mozilla.VerifySessionContext
	if totp {
		verify = mozilla.VerifyAuthenticator
	}
	if err := verify(ctx, session, cleanEmail(code)); err != nil {
		return nil, err
	}
	a.mu.Lock()
	if a.pendingSession != session {
		a.mu.Unlock()
		return nil, errors.New("sign-in expired, please start again")
	}
	a.pendingVerified = true
	a.mu.Unlock()
	return a.ContinueVerifiedLogin(ctx)
}

// ContinueVerifiedLogin retries the token exchange without resubmitting the
// one-time code after Firefox Accounts has already accepted it.
func (a *Auth) ContinueVerifiedLogin(ctx context.Context) (*LoginStep, error) {
	a.mu.Lock()
	session := a.pendingSession
	verified := a.pendingVerified
	a.mu.Unlock()
	if session == "" || !verified {
		return nil, errors.New("verify the sign-in session before continuing")
	}
	step, err := a.finish(ctx, session)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &LoginStep{Step: "verified", Message: err.Error()}, nil
	}
	return step, nil
}

func (a *Auth) ConfirmLink(ctx context.Context) (*LoginStep, error) {
	a.mu.Lock()
	session := a.pendingSession
	a.mu.Unlock()
	if session == "" {
		return nil, errors.New("sign-in expired, please start again")
	}
	step, err := a.finish(ctx, session)
	if err != nil {
		return nil, errors.New("not confirmed yet: open the link in the email first")
	}
	return step, nil
}

func (a *Auth) SubmitUnblock(ctx context.Context, code string) (*LoginStep, error) {
	a.mu.Lock()
	email, pw := a.pendingEmail, a.pendingPassword
	a.mu.Unlock()
	if pw == "" {
		return nil, errors.New("sign-in expired, please start again")
	}
	return a.login(ctx, email, pw, strings.TrimSpace(code))
}

func (a *Auth) CancelLogin() {
	a.mu.Lock()
	a.pendingEmail, a.pendingPassword, a.pendingSession = "", "", ""
	a.pendingVerified = false
	a.pendingTOTP = false
	a.mu.Unlock()
}
