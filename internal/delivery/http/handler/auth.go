package handler

import (
	"errors"
	"net/http"
	"strings"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/helper"
	"third-party-review/internal/service/auth"
)

type loginView struct {
	Username string
	Next     string
	Error    string
	// FirstRun switches the page into the create-the-first-account form.
	FirstRun bool
}

type mfaView struct {
	Next  string
	Error string
}

// LoginPage renders the sign-in form, or the first-run setup form when no
// account exists yet.
func (h *Handler) LoginPage(w http.ResponseWriter, r *http.Request) {
	if middleware.UserFrom(r.Context()) != nil {
		h.redirect(w, r, "/assessments")
		return
	}

	// A pending session means the password step is done and only the code is
	// outstanding; send the user to the code prompt rather than making them
	// type the password again.
	if _, err := h.auth.PendingSession(r.Context(), middleware.SessionIDFrom(r)); err == nil {
		h.redirect(w, r, "/login/mfa")
		return
	}

	n, err := h.auth.UserCount(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPage(w, r, http.StatusOK, "login", pageData{
		Title:  "Sign in",
		Active: "login",
		Data:   loginView{Next: safeNext(r.URL.Query().Get("next")), FirstRun: n == 0},
	})
}

// Login verifies the credentials and opens a session.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	username := r.FormValue("username")
	next := safeNext(r.FormValue("next"))

	res, err := h.auth.Login(r.Context(), username, r.FormValue("password"),
		r.UserAgent(), middleware.ClientIP(r))
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			h.renderPage(w, r, http.StatusUnauthorized, "login", pageData{
				Title:  "Sign in",
				Active: "login",
				Data: loginView{
					Username: username,
					Next:     next,
					Error:    "That username and password don't match.",
				},
			})
			return
		}
		h.fail(w, r, err)
		return
	}

	if res.MFARequired {
		// A short-lived cookie for the half-finished login: it authorises
		// nothing until the code is verified.
		middleware.SetSessionCookie(w, r, res.Session.ID, 300)
		h.redirect(w, r, "/login/mfa?next="+urlEscape(next))
		return
	}

	middleware.SetSessionCookie(w, r, res.Session.ID, int(h.sessionTTL.Seconds()))
	h.redirect(w, r, next)
}

// MFAPage prompts for the authenticator code.
func (h *Handler) MFAPage(w http.ResponseWriter, r *http.Request) {
	if _, err := h.auth.PendingSession(r.Context(), middleware.SessionIDFrom(r)); err != nil {
		h.redirect(w, r, "/login")
		return
	}
	h.renderPage(w, r, http.StatusOK, "mfa", pageData{
		Title:  "Two-factor authentication",
		Active: "login",
		Data:   mfaView{Next: safeNext(r.URL.Query().Get("next"))},
	})
}

// VerifyMFA completes a pending login.
func (h *Handler) VerifyMFA(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	sessionID := middleware.SessionIDFrom(r)
	next := safeNext(r.FormValue("next"))

	if _, err := h.auth.VerifyMFA(r.Context(), sessionID, r.FormValue("code")); err != nil {
		switch {
		case errors.Is(err, auth.ErrInvalidMFACode):
			h.renderPage(w, r, http.StatusUnauthorized, "mfa", pageData{
				Title:  "Two-factor authentication",
				Active: "login",
				Data: mfaView{
					Next:  next,
					Error: "That code isn't right. Codes change every 30 seconds - try the current one, or use a recovery code.",
				},
			})
		case errors.Is(err, auth.ErrInvalidCredentials):
			middleware.ClearSessionCookie(w, r)
			h.redirect(w, r, "/login")
		default:
			h.fail(w, r, err)
		}
		return
	}

	// Re-issue the cookie with the full session lifetime.
	middleware.SetSessionCookie(w, r, sessionID, int(h.sessionTTL.Seconds()))
	h.redirect(w, r, next)
}

// Logout ends the session.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	if err := h.auth.Logout(r.Context(), middleware.SessionIDFrom(r)); err != nil {
		h.log.Warn("could not delete the session on logout", "error", err)
	}
	middleware.ClearSessionCookie(w, r)
	h.redirect(w, r, "/login")
}

// FirstRunSetup creates the first account when the database has none. It is
// refused the moment any user exists, so it cannot be used to add an account
// later.
func (h *Handler) FirstRunSetup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	n, err := h.auth.UserCount(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if n > 0 {
		h.redirect(w, r, "/login")
		return
	}

	username := r.FormValue("username")
	password := r.FormValue("password")
	if password != r.FormValue("password_confirm") {
		h.renderPage(w, r, http.StatusUnprocessableEntity, "login", pageData{
			Title: "Set up", Active: "login",
			Data: loginView{Username: username, FirstRun: true, Error: "The two passwords don't match."},
		})
		return
	}

	if _, err := h.auth.CreateUser(r.Context(), username, r.FormValue("display_name"), password); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderPage(w, r, http.StatusUnprocessableEntity, "login", pageData{
				Title: "Set up", Active: "login",
				Data: loginView{Username: username, FirstRun: true, Error: ve.Message},
			})
			return
		}
		h.fail(w, r, err)
		return
	}

	res, err := h.auth.Login(r.Context(), username, password, r.UserAgent(), middleware.ClientIP(r))
	if err != nil {
		h.redirect(w, r, "/login")
		return
	}
	middleware.SetSessionCookie(w, r, res.Session.ID, int(h.sessionTTL.Seconds()))
	h.redirect(w, r, "/assessments")
}

// safeNext sanitises a post-login redirect target.
//
// Only a same-site absolute path is allowed. Without this check, a link to
// /login?next=https://evil.example turns the app's own login page into an
// open redirect that lends it credibility.
func safeNext(next string) string {
	next = strings.TrimSpace(next)
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/assessments"
	}
	if strings.Contains(next, "\\") || strings.ContainsAny(next, "\r\n") {
		return "/assessments"
	}
	return next
}

// urlEscape percent-encodes a query value.
func urlEscape(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~', c == '/':
			b.WriteByte(c)
		default:
			const hex = "0123456789ABCDEF"
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0x0f])
		}
	}
	return b.String()
}
