package handler

import "net/http"

// DashboardPage renders the home page: cross-assessment counts, risk
// distribution, recent activity and anything that needs a reviewer's
// attention.
func (h *Handler) DashboardPage(w http.ResponseWriter, r *http.Request) {
	view, err := h.dashboards.Build(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPage(w, r, http.StatusOK, "dashboard", pageData{
		Title:  "Dashboard",
		Active: "dashboard",
		Data:   view,
	})
}
