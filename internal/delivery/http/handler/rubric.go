package handler

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// AttachRubric saves a pasted or uploaded rubric and attaches it to the
// assessment, so the next review compares answers against it.
func (h *Handler) AttachRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	// Fetched up front only for the default name below - an assessment and
	// its vendor read far better there than the assessment's bare id.
	assessment, err := h.assessments.GetAssessment(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// Accept either a pasted textarea or an uploaded text file.
	content := ""
	name := strings.TrimSpace(r.FormValue("name"))

	if err := r.ParseMultipartForm(8 << 20); err == nil && r.MultipartForm != nil {
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		if file, header, ferr := r.FormFile("file"); ferr == nil {
			defer file.Close()
			buf := make([]byte, 1<<20)
			n, _ := file.Read(buf)
			content = string(buf[:n])
			if name == "" {
				name = header.Filename
			}
		}
	}
	if strings.TrimSpace(content) == "" {
		content = r.FormValue("content")
	}
	if name == "" {
		name = fmt.Sprintf("Rubric for %s (%s)", assessment.Title, assessment.VendorName)
	}

	rubric := &model.Rubric{
		Name:     name,
		Content:  content,
		Reusable: r.FormValue("reusable") == "on" || r.FormValue("reusable") == "1",
	}
	if err := h.assessments.AttachRubric(r.Context(), id, rubric); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", rubricView{AssessmentID: id, Rubric: rubric})
}

// EditRubric swaps the read-only rubric summary for the edit form,
// pre-filled with its current name, content and reusable flag.
func (h *Handler) EditRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rubric, err := h.assessments.GetRubric(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", rubricView{AssessmentID: id, Rubric: rubric, Editing: true})
}

// CancelEditRubric reverts to the read-only summary without saving anything.
func (h *Handler) CancelEditRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view := rubricView{AssessmentID: id}
	rubric, err := h.assessments.GetRubric(r.Context(), id)
	if err == nil {
		view.Rubric = rubric
	} else if !errors.Is(err, helper.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", view)
}

// UpdateRubric saves edits to the assessment's currently attached rubric.
func (h *Handler) UpdateRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	current, err := h.assessments.GetRubric(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	updated := &model.Rubric{
		ID:       current.ID,
		Name:     strings.TrimSpace(r.FormValue("name")),
		Content:  r.FormValue("content"),
		Reusable: r.FormValue("reusable") == "on" || r.FormValue("reusable") == "1",
	}
	if err := h.assessments.UpdateRubric(r.Context(), updated); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", rubricView{AssessmentID: id, Rubric: updated})
}

// DetachRubric removes the rubric from an assessment. The rubric itself is
// kept if it was marked reusable.
func (h *Handler) DetachRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.assessments.DetachRubric(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", rubricView{AssessmentID: id})
}

type rubricView struct {
	AssessmentID uuid.UUID
	Rubric       *model.Rubric
	Reusable     []*model.Rubric
	// Editing shows the edit form in place of the read-only summary.
	Editing bool
}

// RubricPanel renders the current rubric state for the assessment page.
func (h *Handler) RubricPanel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view := rubricView{AssessmentID: id}
	rubric, err := h.assessments.GetRubric(r.Context(), id)
	if err == nil {
		view.Rubric = rubric
	} else if !errors.Is(err, helper.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", view)
}

func parseUUID(s string) (uuid.UUID, bool) {
	v, err := uuid.Parse(strings.TrimSpace(s))
	if err != nil || v == uuid.Nil {
		return uuid.Nil, false
	}
	return v, true
}
