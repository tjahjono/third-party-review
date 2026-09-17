package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

// revisionPreviewView is what the confirmation screen renders from, after a
// re-uploaded questionnaire has been matched against an assessment's
// existing questions.
type revisionPreviewView struct {
	Assessment *model.Assessment
	Preview    *dto.AnswerRevisionPreview
}

// UploadAnswerRevisions accepts a questionnaire the vendor has revised and
// returned, matches its rows against the assessment's existing questions,
// and shows what would change. Nothing is written here - ApplyAnswerRevisions
// below does that, once the assessor has confirmed which rows to apply.
func (h *Handler) UploadAnswerRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	a, err := h.assessments.GetAssessment(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		h.renderRevisionError(w, r, a,
			"The upload could not be read. It may be larger than the configured limit.")
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	file, header, err := r.FormFile("file")
	if err != nil {
		h.renderRevisionError(w, r, a, "Choose a file to upload.")
		return
	}
	defer file.Close()

	preview, err := h.assessments.PreviewAnswerRevisions(r.Context(), id, header.Filename, file)
	if err != nil {
		h.renderRevisionError(w, r, a, friendlyUploadError(err))
		return
	}

	h.renderPage(w, r, http.StatusOK, "revision_preview", pageData{
		Title:  "Revised answers - " + a.Title,
		Active: "assessments",
		Data:   revisionPreviewView{Assessment: a, Preview: preview},
	})
}

// renderRevisionError re-renders the revision_preview page with no preview
// and an explanatory message, so a bad upload has somewhere to go back to
// rather than a bare error fragment.
func (h *Handler) renderRevisionError(w http.ResponseWriter, r *http.Request, a *model.Assessment, message string) {
	h.renderPage(w, r, http.StatusUnprocessableEntity, "revision_preview", pageData{
		Title:  "Revised answers - " + a.Title,
		Active: "assessments",
		Error:  message,
		Data:   revisionPreviewView{Assessment: a},
	})
}

// friendlyUploadError extracts a user-facing message from a parse or
// validation failure, falling back to a generic message for anything else -
// this page has no field-level rendering, so an internal error is reported
// the same way an unreadable file is, without leaking details.
func friendlyUploadError(err error) string {
	var ve helper.ValidationError
	if errors.As(err, &ve) {
		return ve.Message
	}
	var pe *helper.ParseError
	if errors.As(err, &pe) {
		return pe.Message
	}
	return "That file could not be processed."
}

// ApplyAnswerRevisions writes the confirmed answer changes and returns to the
// assessment page. The confirmation form posts one apply_<questionID>
// checkbox per row the assessor left checked, alongside a matching
// answer_<questionID> hidden field carrying the new text - a row left
// unchecked is simply absent from the applied set.
func (h *Handler) ApplyAnswerRevisions(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	var revisions []dto.AnswerRevisionInput
	for key := range r.PostForm {
		qIDRaw, ok := strings.CutPrefix(key, "apply_")
		if !ok {
			continue
		}
		qID, err := uuid.Parse(strings.TrimSpace(qIDRaw))
		if err != nil || qID == uuid.Nil {
			continue
		}
		revisions = append(revisions, dto.AnswerRevisionInput{
			QuestionID: qID,
			NewAnswer:  r.PostForm.Get("answer_" + qIDRaw),
		})
	}

	applied, err := h.assessments.ApplyAnswerRevisions(r.Context(), id, revisions)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if applied > 0 {
		if _, err := h.reviews.RecomputeSummary(r.Context(), id); err != nil {
			h.log.Warn("could not refresh the summary after applying revised answers",
				"assessment_id", id, "error", err)
		}
	}
	h.redirect(w, r, "/assessments/"+uuidStr(id)+"?revised="+strconv.Itoa(applied))
}
