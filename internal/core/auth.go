package core

import (
	"errors"
	"strings"
	"sync"
	"time"

	"zeusvpn/internal/mozilla"
	"zeusvpn/internal/store"
)

var ErrNotLoggedIn = errors.New("not signed in")

// LoginStep tells the UI what the sign-in flow needs next.
type LoginStep struct {
	Step    string `json:"step"` // "done", "code", "link", "unblock"
	Message string `json:"message"`
}

type Auth struct {
	mu     sync.Mutex
	tokens *store.Tokens

	pendingEmail    string
	pendingPassword string
	pendingSession  string
}

func NewAuth() *Auth { return &Auth{tokens: store.LoadTokens()} }

func (a *Auth) LoggedIn() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.tokens != nil && (a.tokens.AccessToken != "" || a.tokens.RefreshToken != "")
}

// AccessToken returns a valid OAuth access token, refreshing it if needed.
func (a *Auth) AccessToken() (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tokens == nil {
		return "", ErrNotLoggedIn
	}
	if a.tokens.Valid() {
		return a.tokens.AccessToken, nil
	}
	if a.tokens.RefreshToken == "" {
		return "", errors.New("your session expired, please sign in again")
	}
	t, err := mozilla.RefreshToken(a.tokens.RefreshToken)
	if err != nil {
		var fe *mozilla.FxaError
		if errors.As(err, &fe) && fe.Status >= 400 && fe.Status < 500 {
			return "", errors.New("your session expired, please sign in again")
		}
		return "", err
	}
	a.saveLocked(t)
	return t.AccessToken, nil
}

func (a *Auth) saveLocked(t *mozilla.TokenResponse) {
	a.tokens = &store.Tokens{AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, Scope: t.Scope, ObtainedAt: time.Now(), ExpiresIn: t.ExpiresIn}
	store.SaveTokens(a.tokens)
}

func (a *Auth) finish(session string) (*LoginStep, error) {
	t, err := mozilla.OAuthToken(session)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.saveLocked(t)
	a.pendingPassword, a.pendingSession = "", ""
	a.mu.Unlock()
	return &LoginStep{Step: "done"}, nil
}

func (a *Auth) Login(email, password string) (*LoginStep, error) {
	email = strings.TrimSpace(email)
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
	if err := mozilla.VerifySession(session, strings.TrimSpace(code)); err != nil {
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

func (a *Auth) Logout() {
	a.mu.Lock()
	t := a.tokens
	a.tokens = nil
	a.pendingEmail, a.pendingPassword, a.pendingSession = "", "", ""
	a.mu.Unlock()
	store.DeleteTokens()
	if t != nil && t.RefreshToken != "" {
		go mozilla.DestroyToken(t.RefreshToken)
	}
}
