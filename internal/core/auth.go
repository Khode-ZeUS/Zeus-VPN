package core

import (
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
	Step    string `json:"step"` // "done", "code", "link", "unblock"
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
			QuotaLeft:  acc.QuotaLeft,
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
	acc.QuotaLimit, acc.QuotaLeft, acc.QuotaAt = p.QuotaLimit, p.QuotaLeft, time.Now()
	if t, err := time.Parse(time.RFC3339, p.QuotaReset); err == nil {
		acc.QuotaReset = t
	}
	acc.Exhausted = p.QuotaLeft <= 0
	a.saveLocked()
}

// MarkExhausted records that Mozilla refused the account for lack of data.
func (a *Auth) MarkExhausted(email string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if acc := a.accs.Find(email); acc != nil {
		acc.Exhausted = true
		acc.QuotaLeft = 0
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

func (a *Auth) finish(session string) (*LoginStep, error) {
	t, err := mozilla.OAuthToken(session)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
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
	a.pendingPassword, a.pendingSession = "", ""
	return &LoginStep{Step: "done"}, nil
}

func (a *Auth) Login(email, password string) (*LoginStep, error) {
	email = cleanEmail(email)
	if email == "" || password == "" {
		return nil, errors.New("enter your email and password")
	}
	return a.login(email, password, "")
}

func (a *Auth) login(email, password, unblock string) (*LoginStep, error) {
	resp, err := mozilla.Login(email, password, unblock)
	if err != nil {
		var fe *mozilla.FxaError
		if errors.As(err, &fe) && fe.Errno == mozilla.ErrnoRequestBlocked {
			a.mu.Lock()
			a.pendingEmail, a.pendingPassword = email, password
			a.mu.Unlock()
			if serr := mozilla.SendUnblockCode(email); serr != nil {
				return nil, serr
			}
			return &LoginStep{Step: "unblock", Message: "Mozilla blocked this sign-in for safety. Enter the unblock code sent to " + email + "."}, nil
		}
		return nil, err
	}
	a.mu.Lock()
	a.pendingEmail, a.pendingSession = email, resp.SessionToken
	a.mu.Unlock()
	if !resp.Verified {
		if resp.VerificationMethod == "email" {
			return &LoginStep{Step: "link", Message: "Open the confirmation link sent to " + email + ", then continue."}, nil
		}
		return &LoginStep{Step: "code", Message: "Enter the 6-digit code sent to " + email + "."}, nil
	}
	return a.finish(resp.SessionToken)
}

func (a *Auth) PendingEmail() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pendingEmail
}

func (a *Auth) VerifyCode(code string) (*LoginStep, error) {
	a.mu.Lock()
	session := a.pendingSession
	a.mu.Unlock()
	if session == "" {
		return nil, errors.New("sign-in expired, please start again")
	}
	if err := mozilla.VerifySession(session, cleanEmail(code)); err != nil {
		return nil, err
	}
	return a.finish(session)
}

func (a *Auth) ConfirmLink() (*LoginStep, error) {
	a.mu.Lock()
	session := a.pendingSession
	a.mu.Unlock()
	if session == "" {
		return nil, errors.New("sign-in expired, please start again")
	}
	step, err := a.finish(session)
	if err != nil {
		return nil, errors.New("not confirmed yet: open the link in the email first")
	}
	return step, nil
}

func (a *Auth) SubmitUnblock(code string) (*LoginStep, error) {
	a.mu.Lock()
	email, pw := a.pendingEmail, a.pendingPassword
	a.mu.Unlock()
	if pw == "" {
		return nil, errors.New("sign-in expired, please start again")
	}
	return a.login(email, pw, strings.TrimSpace(code))
}

func (a *Auth) CancelLogin() {
	a.mu.Lock()
	a.pendingEmail, a.pendingPassword, a.pendingSession = "", "", ""
	a.mu.Unlock()
}
