package web

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/lassegit/neolib/internal/auth"
	"github.com/lassegit/neolib/internal/config"
	"github.com/lassegit/neolib/internal/store"
)

func (s *Server) getSignin(w http.ResponseWriter, r *http.Request) {
	if _, err := s.auth.User(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	} else if !errors.Is(err, auth.ErrNoSession) {
		s.serverError(w, r, err)
		return
	}

	page := signinPage{baseData: s.base(w, r, "Sign in")}
	page.Next = safeNext(r.URL.Query().Get("next"))
	s.render(w, r, http.StatusOK, "signin", page)
}

func (s *Server) postSignin(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}

	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	password := r.FormValue("password")
	next := safeNext(r.FormValue("next"))

	if email == "" || password == "" {
		page := signinPage{baseData: s.base(w, r, "Sign in"), Email: email, Next: next}
		page.Error = "Enter your email address and password."
		s.render(w, r, http.StatusBadRequest, "signin", page)
		return
	}

	if _, err := s.auth.SignIn(r.Context(), w, r, email, password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			page := signinPage{baseData: s.base(w, r, "Sign in"), Email: email, Next: next}
			page.Error = "The email address or password is incorrect."
			s.render(w, r, http.StatusUnauthorized, "signin", page)
			return
		}
		s.serverError(w, r, err)
		return
	}

	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) getSignup(w http.ResponseWriter, r *http.Request) {
	if _, err := s.auth.User(r); err == nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	} else if !errors.Is(err, auth.ErrNoSession) {
		s.serverError(w, r, err)
		return
	}

	allowed, err := s.signupAllowed(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	page := signupPage{baseData: s.base(w, r, "Create account"), Closed: !allowed}
	s.render(w, r, http.StatusOK, "signup", page)
}

func (s *Server) postSignup(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}

	allowed, err := s.signupAllowed(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !allowed {
		page := signupPage{baseData: s.base(w, r, "Create account"), Closed: true}
		s.render(w, r, http.StatusForbidden, "signup", page)
		return
	}

	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	page := signupPage{baseData: s.base(w, r, "Create account"), Email: email, DisplayName: displayName}
	fail := func(message string) {
		page.Error = message
		s.render(w, r, http.StatusBadRequest, "signup", page)
	}

	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email {
		fail("Enter a valid email address.")
		return
	}
	if len([]rune(displayName)) > 100 {
		fail("The display name is too long.")
		return
	}
	if len(password) < 8 {
		fail("The password must be at least 8 characters.")
		return
	}
	if password != confirm {
		fail("The passwords do not match.")
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	user, err := s.store.CreateUser(r.Context(), email, displayName, hash)
	if errors.Is(err, store.ErrDuplicate) {
		fail("An account with that email address already exists.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	if err := s.auth.NewSession(r.Context(), w, r, user.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) signout(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	if err := s.auth.SignOut(r.Context(), w, r); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/signin", http.StatusSeeOther)
}

// signupAllowed applies the configured registration policy.
func (s *Server) signupAllowed(r *http.Request) (bool, error) {
	switch s.cfg.Signup {
	case config.SignupAlways:
		return true, nil
	case config.SignupNever:
		return false, nil
	}
	count, err := s.store.CountUsers(r.Context())
	if err != nil {
		return false, err
	}
	return count == 0, nil
}

// safeNext only allows local paths, preventing open redirects.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}
