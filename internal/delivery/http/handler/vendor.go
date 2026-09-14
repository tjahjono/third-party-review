package handler

import (
	"net/http"
	"strings"

	"third-party-review/internal/domain"
)

type vendorListView struct {
	Vendors []*domain.Vendor
	Total   int
	Search  string
}

// ListVendors renders the vendor index.
func (h *Handler) ListVendors(w http.ResponseWriter, r *http.Request) {
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	vendors, total, err := h.Assessments.ListVendors(r.Context(), search, 200, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view := vendorListView{Vendors: vendors, Total: total, Search: search}

	// A search box typing into HTMX swaps only the table.
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("partial") == "1" {
		h.renderPartial(w, r, http.StatusOK, "vendor_table", view)
		return
	}
	h.renderPage(w, r, http.StatusOK, "vendors", pageData{
		Title:  "Vendors",
		Active: "vendors",
		Data:   view,
	})
}

// CreateVendor registers a new third party and swaps the refreshed table back.
func (h *Handler) CreateVendor(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, domain.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	v := &domain.Vendor{
		Name:         r.FormValue("name"),
		ContactName:  r.FormValue("contact_name"),
		ContactEmail: r.FormValue("contact_email"),
		Notes:        r.FormValue("notes"),
	}
	if err := h.Assessments.CreateVendor(r.Context(), v); err != nil {
		h.fail(w, r, err)
		return
	}

	vendors, total, err := h.Assessments.ListVendors(r.Context(), "", 200, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "vendor_table", vendorListView{Vendors: vendors, Total: total})
}

// DeleteVendor removes a vendor and its assessments.
func (h *Handler) DeleteVendor(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "vendorID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Assessments.DeleteVendor(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	vendors, total, err := h.Assessments.ListVendors(r.Context(), "", 200, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "vendor_table", vendorListView{Vendors: vendors, Total: total})
}
