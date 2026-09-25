// Package auth owns password hashing, session cookies, and CSRF tokens.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lassegit/neolib/internal/config"
	"github.com/lassegit/neolib/internal/store"
)

const (
	sessionCookieName = "neolib_session"
	csrfCookieName    = "neolib_csrf"
)

// Errors returned by Manager.
var (
	// ErrInvalidCredentials is intentionally indistinguishable between an
	// unknown email and a wrong password.
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	// ErrNoSession means the request has no valid session cookie.
	ErrNoSession = errors.New("auth: no session")
)

// Options configures the auth manager.
type Options struct {
	SecureCookies config.SecureMode
	SessionTTL    time.Duration
}

// Manager ties password verification to the store and HTTP cookies.
type Manager struct {
	store     *store.Store
	opts      Options
	dummyHash string
}

// NewManager returns a Manager. It precomputes a dummy hash so that sign-in
// for unknown emails costs the same as for known ones.
func NewManager(st *store.Store, opts Options) *Manager {
	dummy, err := HashPassword("neolib-dummy-password")
	if err != nil {
		panic("auth: cannot compute dummy hash: " + err.Error())
	}
	return &Manager{store: st, opts: opts, dummyHash: dummy}
}

// SignIn verifies credentials and starts a session.
func (m *Manager) SignIn(ctx context.Context, w http.ResponseWriter, r *http.Request, email, password string) (*store.User, error) {
	user, err := m.store.UserByEmail(ctx, email)
	if errors.Is(err, store.ErrNotFound) {
		_, _ = VerifyPassword(m.dummyHash, password) // equalize timing
		return nil, ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}

	ok, err := VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidCredentials
	}

	if err := m.NewSession(ctx, w, r, user.ID); err != nil {
		return nil, err
	}
	return &user, nil
}

// NewSession creates a session for userID and sets the session cookie.
func (m *Manager) NewSession(ctx context.Context, w http.ResponseWriter, r *http.Request, userID string) error {
	token, tokenHash, err := newToken()
	if err != nil {
		return err
	}
	now := time.Now()
	if err := m.store.CreateSession(ctx, tokenHash, userID, now.Unix(), now.Add(m.opts.SessionTTL).Unix()); err != nil {
		return err
	}
	http.SetCookie(w, m.sessionCookie(r, token, int(m.opts.SessionTTL.Seconds())))
	return nil
}

// User returns the user for the request's session, or ErrNoSession.
func (m *Manager) User(r *http.Request) (*store.User, error) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return nil, ErrNoSession
	}
	user, err := m.store.SessionUser(r.Context(), HashToken(cookie.Value), time.Now().Unix())
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

// SignOut deletes the current session and clears the cookie.
func (m *Manager) SignOut(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
		if err := m.store.DeleteSession(ctx, HashToken(cookie.Value)); err != nil {
			return err
		}
	}
	http.SetCookie(w, m.sessionCookie(r, "", -1))
	return nil
}

// DeleteAllSessions removes every session for a user (used after a password
// change).
func (m *Manager) DeleteAllSessions(ctx context.Context, userID string) error {
	return m.store.DeleteUserSessions(ctx, userID)
}

// CSRFToken returns the double-submit token for this request, creating the
// cookie if needed.
func (m *Manager) CSRFToken(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && len(cookie.Value) >= 32 {
		return cookie.Value
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("auth: crypto/rand failed: " + err.Error())
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.secure(r),
	})
	return token
}

// HasCSRFCookie reports whether the request carries a CSRF cookie. Handlers
// can use it to reject submissions that cannot possibly validate before
// reading a potentially large request body.
func (m *Manager) HasCSRFCookie(r *http.Request) bool {
	cookie, err := r.Cookie(csrfCookieName)
	return err == nil && cookie.Value != ""
}

// ValidCSRF reports whether formToken matches the CSRF cookie.
func (m *Manager) ValidCSRF(r *http.Request, formToken string) bool {
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil || formToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(formToken)) == 1
}

// HashToken hashes a raw session token for storage.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (token, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

func (m *Manager) sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.secure(r),
	}
}

func (m *Manager) secure(r *http.Request) bool {
	switch m.opts.SecureCookies {
	case config.SecureAlways:
		return true
	case config.SecureNever:
		return false
	}
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
