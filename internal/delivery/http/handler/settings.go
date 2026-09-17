package handler

import (
	"errors"
	"net/http"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

// bandPreviewRow is one line of the settings page's "what this looks like"
// table: a raw score and the band it currently maps to, computed the exact
// same way the rest of the app would show it.
type bandPreviewRow struct {
	Score model.RiskScore
	Band  model.RiskBand
}

type settingsView struct {
	Matrix    *model.RiskMatrix
	Preview   []bandPreviewRow
	UpdatedBy *model.User
	Flash     string
	Error     string
	CSRFToken string
}

// SettingsPage shows the app-wide configuration - currently just the
// severity risk matrix.
func (h *Handler) SettingsPage(w http.ResponseWriter, r *http.Request) {
	matrix, err := h.settings.GetRiskMatrix(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderSettings(w, r, http.StatusOK, settingsView{Matrix: matrix})
}

// UpdateRiskMatrix validates and saves the severity risk matrix, applying it
// immediately.
func (h *Handler) UpdateRiskMatrix(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	matrix := &model.RiskMatrix{
		MediumMin:   model.RiskScore(formInt(r, "medium_min", int(model.DefaultRiskMatrix.MediumMin))),
		HighMin:     model.RiskScore(formInt(r, "high_min", int(model.DefaultRiskMatrix.HighMin))),
		CriticalMin: model.RiskScore(formInt(r, "critical_min", int(model.DefaultRiskMatrix.CriticalMin))),
	}

	userID := middleware.UserIDFrom(r.Context())
	if err := h.settings.UpdateRiskMatrix(r.Context(), matrix, userID); err != nil {
		var ve helper.ValidationError
		if errors.As(err, &ve) {
			h.renderSettings(w, r, http.StatusUnprocessableEntity, settingsView{Matrix: matrix, Error: ve.Message})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.renderSettings(w, r, http.StatusOK, settingsView{
		Matrix: matrix,
		Flash:  "The risk matrix has been updated. Every page now uses it, including ones already open.",
	})
}

// renderSettings fills in the preview table and the signed-in user before
// rendering, so neither handler above has to remember to.
func (h *Handler) renderSettings(w http.ResponseWriter, r *http.Request, status int, view settingsView) {
	view.Preview = make([]bandPreviewRow, 0, int(model.RiskMax))
	for s := model.RiskMin; s <= model.RiskMax; s++ {
		view.Preview = append(view.Preview, bandPreviewRow{Score: s, Band: helper.Band(s)})
	}
	if view.Matrix != nil && view.Matrix.UpdatedBy != nil {
		if u, err := h.auth.GetUser(r.Context(), *view.Matrix.UpdatedBy); err == nil {
			view.UpdatedBy = u
		}
	}
	view.CSRFToken = middleware.CSRFTokenFrom(r.Context())
	h.renderPage(w, r, status, "settings", pageData{
		Title:  "Settings",
		Active: "settings",
		Flash:  view.Flash,
		Data:   view,
	})
}
