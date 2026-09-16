package handler

import (
	"errors"
	"net/http"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service/auth"
)

type accountView struct {
	User *model.User
	// Enrolment is set while an MFA enrolment is in progress.
	Enrolment *dto.MFAEnrolment
	// RecoveryCodes are shown exactly once, immediately after enrolment.
	RecoveryCodes []string
	Flash         string
	Error         string
	CSRFToken     string
}

// AccountPage shows the signed-in user's own settings.
func (h *Handler) AccountPage(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	h.renderAccount(w, r, http.StatusOK, accountView{User: user})
}

// ChangePassword updates the signed-in user's password.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	next := r.FormValue("new_password")
	if next != r.FormValue("new_password_confirm") {
		h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{
			User: user, Error: "The two new passwords don't match.",
		})
		return
	}

	if err := h.auth.ChangePassword(r.Context(), user.ID, r.FormValue("current_password"), next); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{User: user, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderAccount(w, r, http.StatusOK, accountView{User: user, Flash: "Your password has been changed."})
}

// UpdateLanguage sets the language the AI writes future review drafts and
// summaries in for the signed-in user.
func (h *Handler) UpdateLanguage(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	lang := model.Language(r.FormValue("language"))
	if err := h.auth.UpdateLanguage(r.Context(), user.ID, lang); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{User: user, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	refreshed, err := h.auth.GetUser(r.Context(), user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderAccount(w, r, http.StatusOK, accountView{
		User:  refreshed,
		Flash: "AI responses will now be drafted in " + helper.LanguageLabel(refreshed.Language) + ".",
	})
}

// BeginMFA starts authenticator enrolment and shows the QR code.
func (h *Handler) BeginMFA(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	enrolment, err := h.auth.BeginMFAEnrolment(r.Context(), user.ID)
	if err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{User: user, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderAccount(w, r, http.StatusOK, accountView{User: user, Enrolment: enrolment})
}

// ConfirmMFA stores the secret once the user proves they can generate a code
// from it, and shows the recovery codes once.
func (h *Handler) ConfirmMFA(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	secret := r.FormValue("secret")
	codes, err := h.auth.CompleteMFAEnrolment(r.Context(), user.ID, secret, r.FormValue("code"))
	if err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			// Re-render the same enrolment rather than generating a new
			// secret: the user has already added this one to their app, and
			// swapping it under them on a typo is maddening.
			h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{
				User:      user,
				Enrolment: rebuildEnrolment(secret, user.Username),
				Error:     ve.Message,
			})
			return
		}
		h.fail(w, r, err)
		return
	}

	refreshed, err := h.auth.GetUser(r.Context(), user.ID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderAccount(w, r, http.StatusOK, accountView{
		User:          refreshed,
		RecoveryCodes: codes,
		Flash:         "Two-factor authentication is on. Save the recovery codes below - they are shown only once.",
	})
}

// DisableMFA turns the second factor off, after re-checking the password.
func (h *Handler) DisableMFA(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	if err := h.auth.DisableMFA(r.Context(), user.ID, r.FormValue("password")); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{User: user, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	refreshed, _ := h.auth.GetUser(r.Context(), user.ID)
	h.renderAccount(w, r, http.StatusOK, accountView{
		User:  refreshed,
		Flash: "Two-factor authentication is off.",
	})
}

// RegenerateRecoveryCodes issues a fresh set and shows them once.
func (h *Handler) RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	codes, err := h.auth.RegenerateRecoveryCodes(r.Context(), user.ID, r.FormValue("password"))
	if err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderAccount(w, r, http.StatusUnprocessableEntity, accountView{User: user, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderAccount(w, r, http.StatusOK, accountView{
		User:          user,
		RecoveryCodes: codes,
		Flash:         "New recovery codes issued. The previous ones no longer work.",
	})
}

// renderAccount renders the account page with the CSRF token filled in.
func (h *Handler) renderAccount(w http.ResponseWriter, r *http.Request, status int, view accountView) {
	view.CSRFToken = middleware.CSRFTokenFrom(r.Context())
	h.renderPage(w, r, status, "account", pageData{
		Title:  "Your account",
		Active: "account",
		Flash:  view.Flash,
		Data:   view,
	})
}

// rebuildEnrolment recreates the display material for a secret the user is
// part-way through enrolling, so a wrong code does not restart the process.
func rebuildEnrolment(secret, username string) *dto.MFAEnrolment {
	if secret == "" {
		return nil
	}
	return auth.EnrolmentFor(secret, username)
}
