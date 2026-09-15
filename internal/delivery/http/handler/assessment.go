package handler

import (
	"net/http"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type assessmentListView struct {
	Assessments []*model.Assessment
	Vendors     []*model.Vendor
	Total       int
	Search      string
	Status      string
}

// ListAssessments renders the assessment index, which is also the home page.
func (h *Handler) ListAssessments(w http.ResponseWriter, r *http.Request) {
	f := dto.AssessmentFilter{
		Search: strings.TrimSpace(r.URL.Query().Get("q")),
		Limit:  100,
	}
	statusParam := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusParam != "" {
		s := model.AssessmentStatus(statusParam)
		if helper.ValidAssessmentStatus(s) {
			f.Status = &s
		}
	}

	items, total, err := h.assessments.ListAssessments(r.Context(), f)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	vendors, _, err := h.assessments.ListVendors(r.Context(), "", 500, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	view := assessmentListView{
		Assessments: items, Vendors: vendors, Total: total,
		Search: f.Search, Status: statusParam,
	}
	if r.Header.Get("HX-Request") == "true" && r.URL.Query().Get("partial") == "1" {
		h.renderPartial(w, r, http.StatusOK, "assessment_table", view)
		return
	}
	h.renderPage(w, r, http.StatusOK, "assessments", pageData{
		Title:  "Assessments",
		Active: "assessments",
		Data:   view,
	})
}

// NewAssessment renders the upload form.
func (h *Handler) NewAssessment(w http.ResponseWriter, r *http.Request) {
	vendors, _, err := h.assessments.ListVendors(r.Context(), "", 500, 0)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPage(w, r, http.StatusOK, "upload", pageData{
		Title:  "New assessment",
		Active: "assessments",
		Data:   assessmentListView{Vendors: vendors},
	})
}

// UploadAssessment accepts the questionnaire file and starts the mapping flow.
func (h *Handler) UploadAssessment(w http.ResponseWriter, r *http.Request) {
	// The multipart limit is what keeps a large upload off the heap; the
	// service applies its own ceiling as well.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.fail(w, r, helper.ValidationError{
			Field:   "file",
			Message: "The upload could not be read. It may be larger than the configured limit.",
		})
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	vendorID, ok := formID(r, "vendor_id")
	if !ok {
		h.fail(w, r, helper.ValidationError{Field: "vendor_id", Message: "Choose a vendor."})
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		h.fail(w, r, helper.ValidationError{Field: "file", Message: "Choose a questionnaire file to upload."})
		return
	}
	defer file.Close()

	a, err := h.assessments.Upload(r.Context(), vendorID,
		r.FormValue("title"), header.Filename, header.Header.Get("Content-Type"), file)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, "/assessments/"+uuidStr(a.ID)+"/mapping")
}

// DeleteAssessment removes an assessment and everything under it.
func (h *Handler) DeleteAssessment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.assessments.DeleteAssessment(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, "/assessments")
}
