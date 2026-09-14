package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"third-party-review/internal/domain"
)

// AttachRubric saves a pasted or uploaded rubric and attaches it to the
// assessment, so the next review compares answers against it.
func (h *Handler) AttachRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "assessmentID")
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
		name = "Rubric for assessment " + strconv.FormatInt(id, 10)
	}

	rubric := &domain.Rubric{
		Name:     name,
		Content:  content,
		Reusable: r.FormValue("reusable") == "on" || r.FormValue("reusable") == "1",
	}
	if err := h.Assessments.AttachRubric(r.Context(), id, rubric); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", rubricView{AssessmentID: id, Rubric: rubric})
}

// DetachRubric removes the rubric from an assessment. The rubric itself is
// kept if it was marked reusable.
func (h *Handler) DetachRubric(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.Assessments.DetachRubric(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", rubricView{AssessmentID: id})
}

type rubricView struct {
	AssessmentID int64
	Rubric       *domain.Rubric
	Reusable     []*domain.Rubric
}

// RubricPanel renders the current rubric state for the assessment page.
func (h *Handler) RubricPanel(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view := rubricView{AssessmentID: id}
	rubric, err := h.Assessments.GetRubric(r.Context(), id)
	if err == nil {
		view.Rubric = rubric
	} else if !errors.Is(err, domain.ErrNotFound) {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "rubric_panel", view)
}

func parseInt64(s string) (int64, bool) {
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}
