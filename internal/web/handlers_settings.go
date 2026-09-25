package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/lassegit/neolib/internal/auth"
	"github.com/lassegit/neolib/internal/reader"
	"github.com/lassegit/neolib/internal/store"
)

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readerSettings(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := settingsPage{baseData: s.base(w, r, "Settings"), Reader: settings}
	page.Notice = settingsNotice(r)
	s.render(w, r, http.StatusOK, "settings", page)
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	user := userFrom(r)
	displayName := strings.TrimSpace(r.FormValue("display_name"))

	if len([]rune(displayName)) > 100 {
		s.renderSettingsError(w, r, "The display name is too long.")
		return
	}
	if err := s.store.UpdateUserName(r.Context(), user.ID, displayName); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?notice=profile", http.StatusSeeOther)
}

func (s *Server) updatePassword(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	user := userFrom(r)
	current := r.FormValue("current_password")
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	switch {
	case current == "":
		s.renderSettingsError(w, r, "Enter your current password.")
		return
	case len(password) < 8:
		s.renderSettingsError(w, r, "The new password must be at least 8 characters.")
		return
	case password != confirm:
		s.renderSettingsError(w, r, "The new passwords do not match.")
		return
	}

	ok, err := auth.VerifyPassword(user.PasswordHash, current)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if !ok {
		s.renderSettingsError(w, r, "The current password is incorrect.")
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.UpdateUserPassword(r.Context(), user.ID, hash); err != nil {
		s.serverError(w, r, err)
		return
	}

	// Changing the password invalidates every existing session, including
	// this one; a fresh session is issued immediately.
	if err := s.auth.DeleteAllSessions(r.Context(), user.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.auth.NewSession(r.Context(), w, r, user.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?notice=password", http.StatusSeeOther)
}

func (s *Server) renderSettingsError(w http.ResponseWriter, r *http.Request, message string) {
	settings, err := s.readerSettings(r)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	page := settingsPage{baseData: s.base(w, r, "Settings"), Reader: settings}
	page.Error = message
	s.render(w, r, http.StatusBadRequest, "settings", page)
}

// updateReaderSettings validates the reader form and stores it as a JSON
// document, so adding preferences does not require a schema change.
func (s *Server) updateReaderSettings(w http.ResponseWriter, r *http.Request) {
	if !s.checkCSRF(w, r) {
		return
	}
	submitted := reader.Settings{
		ExternalLinks: r.FormValue("external_links"),
		Images:        r.FormValue("images"),
	}
	if submitted.Normalize() != submitted {
		s.renderSettingsError(w, r, "Choose valid reader settings.")
		return
	}
	data, err := submitted.Encode()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.SaveUserSettings(r.Context(), userFrom(r).ID, data); err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings?notice=reader", http.StatusSeeOther)
}

// readerSettings loads the signed-in user's preferences. No stored document
// means defaults; storage or decode failures are returned so the caller can
// surface them instead of silently resetting the user's preferences.
func (s *Server) readerSettings(r *http.Request) (reader.Settings, error) {
	user := userFrom(r)
	if user == nil {
		return reader.Settings{}, errors.New("reader settings: no authenticated user")
	}
	data, err := s.store.UserSettings(r.Context(), user.ID)
	if errors.Is(err, store.ErrNotFound) {
		return reader.DefaultSettings(), nil
	}
	if err != nil {
		return reader.Settings{}, fmt.Errorf("load reader settings: %w", err)
	}
	return reader.DecodeSettings(data)
}

func settingsNotice(r *http.Request) string {
	switch r.URL.Query().Get("notice") {
	case "profile":
		return "Your profile has been updated."
	case "password":
		return "Your password has been changed."
	case "reader":
		return "Your reader settings have been updated."
	}
	return ""
}
