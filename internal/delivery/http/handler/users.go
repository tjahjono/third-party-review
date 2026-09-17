package handler

import (
	"errors"
	"net/http"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

// usersView backs the user management page. There is a single role, so this
// screen is reachable by anyone signed in - Self is who is looking at it, so
// the template can hide "deactivate" on your own row and point you at
// /account instead. Each row's manage/reset forms are always rendered and
// tucked behind a client-side Bootstrap collapse (the same component the nav
// bar already uses) rather than a server round trip, since showing them
// costs nothing to compute.
type usersView struct {
	Users     []*model.User
	Self      *model.User
	Flash     string
	Error     string
	CSRFToken string
}

// UsersPage lists every account.
func (h *Handler) UsersPage(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{Self: self})
}

// CreateAccount adds a new team member.
func (h *Handler) CreateAccount(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	password := r.FormValue("password")
	if password != r.FormValue("password_confirm") {
		h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{
			Self: self, Error: "The two passwords don't match.",
		})
		return
	}

	if _, err := h.auth.CreateUser(r.Context(), r.FormValue("username"), r.FormValue("display_name"), password); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Self: self, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{Self: self, Flash: "Account created."})
}

// RenameAccount updates a user's username and display name.
func (h *Handler) RenameAccount(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "userID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	if err := h.auth.RenameUser(r.Context(), id, r.FormValue("username"), r.FormValue("display_name")); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Self: self, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{Self: self, Flash: "Account updated."})
}

// ResetAccountPassword sets a new password for another account.
func (h *Handler) ResetAccountPassword(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "userID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	next := r.FormValue("password")
	if next != r.FormValue("password_confirm") {
		h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{
			Self: self, Error: "The two passwords don't match.",
		})
		return
	}

	if err := h.auth.AdminResetPassword(r.Context(), id, next); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Self: self, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{
		Self: self, Flash: "Password reset. Share the new password with them directly - it isn't emailed anywhere.",
	})
}

// ResetAccountMFA force-disables two-factor for an account that's locked out.
func (h *Handler) ResetAccountMFA(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "userID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.auth.AdminResetMFA(r.Context(), id); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Self: self, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{Self: self, Flash: "Two-factor authentication turned off for that account."})
}

// DeactivateAccount blocks a login without deleting its history.
func (h *Handler) DeactivateAccount(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "userID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.auth.SetUserActive(r.Context(), id, false, self.ID); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Self: self, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{Self: self, Flash: "Account deactivated. They can no longer sign in."})
}

// ReactivateAccount restores a deactivated account.
func (h *Handler) ReactivateAccount(w http.ResponseWriter, r *http.Request) {
	self, ok := middleware.RequireUser(w, r)
	if !ok {
		return
	}
	id, err := pathID(r, "userID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.auth.SetUserActive(r.Context(), id, true, self.ID); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderUsers(w, r, http.StatusUnprocessableEntity, usersView{Self: self, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderUsers(w, r, http.StatusOK, usersView{Self: self, Flash: "Account reactivated."})
}

// renderUsers reloads the account list fresh from the database and renders
// the full page. Every mutating action above ends here rather than patching
// its own copy of the list in memory, so the screen always reflects what was
// actually committed.
func (h *Handler) renderUsers(w http.ResponseWriter, r *http.Request, status int, view usersView) {
	view.CSRFToken = middleware.CSRFTokenFrom(r.Context())
	if view.Self == nil {
		view.Self = middleware.UserFrom(r.Context())
	}
	users, err := h.auth.ListUsers(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view.Users = users
	h.renderPage(w, r, status, "users", pageData{
		Title:  "User management",
		Active: "users",
		Flash:  view.Flash,
		Data:   view,
	})
}
