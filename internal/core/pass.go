package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"zeusvpn/internal/mozilla"
)

// Below this much remaining data an account counts as used up, so the
// switch happens before traffic starts failing.
const lowQuota = 20 << 20

func quotaText(n int64) string {
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

// passFor gets a proxy pass for one account. A rejected access token is
// refreshed and retried before the VPN entitlement is (re)activated.
func (e *Engine) passFor(email string) (*mozilla.ProxyPass, error) {
	tok, err := e.Auth.AccessToken(email, false)
	if err != nil {
		return nil, err
	}
	pass, err := mozilla.FetchProxyPass(tok)
	if errors.Is(err, mozilla.ErrTokenInvalid) {
		e.Log("info", "Mozilla rejected the access token for "+email+", refreshing it")
		if tok, err = e.Auth.AccessToken(email, true); err != nil {
			return nil, err
		}
		pass, err = mozilla.FetchProxyPass(tok)
		if errors.Is(err, mozilla.ErrTokenInvalid) {
			e.Log("info", "activating the VPN service for "+email)
			if aerr := mozilla.ActivateGuardian(tok); aerr != nil {
				e.Log("warning", "activation failed: "+aerr.Error())
			}
			pass, err = mozilla.FetchProxyPass(tok)
		}
	}
	if err == nil {
		e.Auth.RecordQuota(email, pass)
	}
	return pass, err
}

// switchable reports errors that another account could avoid.
func switchable(err error) bool {
	return errors.Is(err, mozilla.ErrQuotaExceeded) || errors.Is(err, mozilla.ErrTokenInvalid) || errors.Is(err, ErrSessionExpired)
}

// acquirePass gets a pass for the active account. When that account is out
// of data or its sign-in is dead and autoSwitch is on, the best remaining
// account takes over.
func (e *Engine) acquirePass(autoSwitch bool) (*mozilla.ProxyPass, string, error) {
	e.passMu.Lock()
	defer e.passMu.Unlock()
	email := e.Auth.ActiveEmail()
	tried := map[string]bool{}
	var firstErr error
	for {
		tried[email] = true
		pass, err := e.passFor(email)
		exhausted := errors.Is(err, mozilla.ErrQuotaExceeded) || (err == nil && pass.QuotaLimit > 0 && pass.QuotaLeft < lowQuota)
		if exhausted {
			e.Auth.MarkExhausted(email)
			err = mozilla.ErrQuotaExceeded
		}
		if err == nil {
			if email != e.Auth.ActiveEmail() {
				e.Auth.SetActive(email)
			}
			return pass, email, nil
		}
		if firstErr == nil {
			firstErr = err
		}
		if !autoSwitch || !switchable(err) {
			return nil, email, friendlyPassError(err)
		}
		next := e.Auth.NextUsable(tried)
		if next == "" {
			return nil, email, friendlyPassError(firstErr)
		}
		reason, why := "its data is used up", "ran out of data"
		if !errors.Is(err, mozilla.ErrQuotaExceeded) {
			reason, why = "its sign-in is no longer valid", "needs to sign in again"
		}
		e.Log("info", fmt.Sprintf("switching from %s to %s because %s", email, next, reason))
		e.notify(fmt.Sprintf("Switched to %s (%s %s)", next, email, why))
		email = next
	}
}

func friendlyPassError(err error) error {
	switch {
	case errors.Is(err, mozilla.ErrQuotaExceeded):
		return errors.New("your monthly Mozilla VPN data is used up; add another account in Settings")
	case errors.Is(err, mozilla.ErrTokenInvalid):
		return errors.New("Mozilla refused this account; sign out and sign in again")
	}
	return err
}

// applyPass makes a new pass the one used for every new connection.
func (e *Engine) applyPass(pass *mozilla.ProxyPass, email string) {
	e.mu.Lock()
	up := e.up
	e.passExpires = pass.Expires
	if pass.QuotaLimit > 0 {
		e.passLimit, e.passLeft = pass.QuotaLimit, pass.QuotaLeft
	}
	if up == nil {
		// The connection has not started yet: its counters begin at zero.
		e.passUpAt, e.passDownAt = 0, 0
	} else {
		e.passUpAt, e.passDownAt = up.Up.Load(), up.Down.Load()
	}
	e.mu.Unlock()
	if up != nil {
		up.SetToken(pass.Token)
	}
	e.update(func(s *State) {
		s.QuotaLimit, s.QuotaLeft = pass.QuotaLimit, pass.QuotaLeft
		s.Account = email
	})
	select {
	case e.renewKick <- struct{}{}:
	default:
	}
}

// quotaPollPeriod re-reads the server-side remaining data this often while
// connected. A pass lives up to 30 minutes, and its quota headers are
// frozen at fetch time, so the browser extension or another device eating
// into the same account would otherwise stay invisible until the next
// renewal.
const quotaPollPeriod = 5 * time.Minute

// quotaPollLoop refreshes the remaining data of the active account from
// Mozilla while the connection is live.
func (e *Engine) quotaPollLoop(ctx context.Context) {
	t := time.NewTicker(quotaPollPeriod)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		email := e.Auth.ActiveEmail()
		if email == "" {
			continue
		}
		pass, err := e.passFor(email)
		if err != nil {
			if errors.Is(err, mozilla.ErrQuotaExceeded) {
				e.Auth.MarkExhausted(email)
				e.Log("warning", fmt.Sprintf("%s is out of data according to Mozilla", email))
			} else if !errors.Is(err, ErrSessionExpired) {
				e.Log("info", "could not refresh account data: "+err.Error())
			}
			continue
		}
		// Only apply when the server shows a different remaining amount
		// than our live estimate, and the account still is the active one
		// (a switch may have happened while the fetch was in flight).
		if pass.QuotaLimit <= 0 || email != e.Auth.ActiveEmail() {
			continue
		}
		limit, left := e.LiveQuota()
		if limit != pass.QuotaLimit || left != pass.QuotaLeft {
			e.applyPass(pass, email)
			e.Log("info", fmt.Sprintf("data refreshed for %s (%s left)", email, quotaText(pass.QuotaLeft)))
		}
	}
}

// onRejected is called by the tunnel when the proxy refuses the current pass.
func (e *Engine) onRejected(ctx context.Context, status int) (string, error) {
	email := e.Auth.ActiveEmail()
	if status == 402 || status == 429 {
		e.Log("warning", fmt.Sprintf("VPN server reports no data left for %s (HTTP %d)", email, status))
		e.Auth.MarkExhausted(email)
	} else {
		e.Log("warning", fmt.Sprintf("VPN server rejected the proxy pass (HTTP %d), renewing it", status))
	}
	pass, used, err := e.acquirePass(e.Settings().AutoSwitch)
	if err != nil {
		e.Log("error", "could not get a new proxy pass: "+err.Error())
		e.update(func(s *State) { s.Error = err.Error() })
		return "", err
	}
	e.applyPass(pass, used)
	return pass.Token, nil
}

// renewDelay mirrors the mobile client: renew at half the remaining life,
// at least 30 s before expiry, between 15 s and 30 min from now.
func renewDelay(expires time.Time) time.Duration {
	remaining := time.Until(expires)
	d := min(remaining/2, remaining-30*time.Second)
	return max(15*time.Second, min(d, 30*time.Minute))
}

func (e *Engine) renewLoop(ctx context.Context) {
	defer close(e.done)
	for {
		e.mu.Lock()
		wait := renewDelay(e.passExpires)
		e.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-e.renewKick:
			continue
		case <-time.After(wait):
		}
		pass, email, err := e.acquirePass(e.Settings().AutoSwitch)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			e.Log("warning", "proxy pass renewal failed, retrying in 30s: "+err.Error())
			e.mu.Lock()
			e.passExpires = time.Now().Add(time.Minute)
			e.mu.Unlock()
			continue
		}
		e.applyPass(pass, email)
		e.Log("info", fmt.Sprintf("proxy pass renewed for %s (%s left), valid until %s",
			email, quotaText(pass.QuotaLeft), pass.Expires.Local().Format("15:04:05")))
	}
}

// RefreshActiveQuota re-reads the server's authoritative remaining data for
// the account in use. Mozilla counts every client of the account, so this
// keeps the displayed number honest while other devices or browser
// extensions consume the same data. Called periodically while connected.
func (e *Engine) RefreshActiveQuota() {
	e.mu.Lock()
	st := e.state.Status
	email := e.state.Account
	e.mu.Unlock()
	if (st != StatusConnected && st != StatusReconnecting) || email == "" {
		return
	}
	pass, err := e.passFor(email)
	if err != nil || pass.QuotaLimit <= 0 {
		return
	}
	e.mu.Lock()
	up := e.up
	e.passLimit, e.passLeft = pass.QuotaLimit, pass.QuotaLeft
	if up == nil {
		e.passUpAt, e.passDownAt = 0, 0
	} else {
		e.passUpAt, e.passDownAt = up.Up.Load(), up.Down.Load()
	}
	e.mu.Unlock()
	e.update(func(s *State) {
		s.QuotaLimit, s.QuotaLeft = pass.QuotaLimit, pass.QuotaLeft
	})
}

// UseAccount makes email the active account; a live connection moves to it
// without reconnecting.
func (e *Engine) UseAccount(email string) error {
	if err := e.Auth.SetActive(email); err != nil {
		return err
	}
	if st := e.State().Status; st != StatusConnected && st != StatusReconnecting {
		e.update(func(s *State) { s.Account = email })
		return nil
	}
	pass, used, err := e.acquirePass(false)
	if err != nil {
		return err
	}
	e.applyPass(pass, used)
	e.Log("info", "now using account "+used)
	return nil
}

// RefreshQuotas updates the remaining data of every account.
func (e *Engine) RefreshQuotas() {
	for _, acc := range e.Auth.Accounts() {
		if acc.NeedsLogin {
			continue
		}
		if _, err := e.passFor(acc.Email); err != nil {
			if errors.Is(err, mozilla.ErrQuotaExceeded) {
				e.Auth.MarkExhausted(acc.Email)
			} else if !errors.Is(err, ErrSessionExpired) {
				e.Log("warning", "could not check data for "+acc.Email+": "+err.Error())
			}
		}
	}
}
