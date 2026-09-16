package handler

import (
	"bytes"
	"net/http"
	"strconv"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service/assessment"

	"github.com/google/uuid"
)

// questionCardView is what a single question card renders from. Cards are
// swapped individually so signing off one finding does not re-render the
// hundred others on the page.
type questionCardView struct {
	Question *model.Question
	// Editing switches the card into the inline editor.
	Editing bool
	// Saved shows a brief confirmation after a sign-off.
	Saved bool
	// Closed disables every action, because the assessment is the signed
	// record now.
	Closed bool
}

// EditQuestion swaps one question card into its inline editor.
func (h *Handler) EditQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "questionID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q, err := h.assessments.GetQuestion(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	closed, err := h.assessmentClosed(r, q.AssessmentID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "question_card", questionCardView{
		Question: q, Editing: !closed, Closed: closed,
	})
}

// CancelEditQuestion swaps the editor back to the read-only card, discarding
// whatever was typed.
func (h *Handler) CancelEditQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "questionID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q, err := h.assessments.GetQuestion(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	closed, _ := h.assessmentClosed(r, q.AssessmentID)
	h.renderPartial(w, r, http.StatusOK, "question_card", questionCardView{Question: q, Closed: closed})
}

// FinalizeQuestion records the reviewer's sign-off and swaps the card back.
func (h *Handler) FinalizeQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "questionID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	userID := middleware.UserIDFrom(r.Context())
	q, err := h.assessments.FinalizeQuestion(r.Context(), id, r.FormValue("feedback"), userID)
	if err != nil {
		h.fail(w, r, err)
		return
	}

	// The summary's sign-off counters are now stale, so recompute them. This
	// is arithmetic over rows already in the database - no AI call.
	if _, err := h.reviews.RecomputeSummary(r.Context(), q.AssessmentID); err != nil {
		h.log.Warn("could not refresh the summary after sign-off",
			"assessment_id", q.AssessmentID, "error", err)
	}

	// Tell the page to refresh the progress bar and summary out of band.
	w.Header().Set("HX-Trigger", "signOffChanged")
	h.renderPartial(w, r, http.StatusOK, "question_card", questionCardView{Question: q, Saved: true})
}

// ReopenQuestion takes a signed-off question back into the draft state.
func (h *Handler) ReopenQuestion(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "questionID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	q, err := h.assessments.ReopenQuestion(r.Context(), id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err := h.reviews.RecomputeSummary(r.Context(), q.AssessmentID); err != nil {
		h.log.Warn("could not refresh the summary after reopening",
			"assessment_id", q.AssessmentID, "error", err)
	}
	w.Header().Set("HX-Trigger", "signOffChanged")
	h.renderPartial(w, r, http.StatusOK, "question_card", questionCardView{Question: q, Editing: true})
}

// BulkFinalize accepts every remaining AI draft, optionally scoped to one
// domain, and re-renders the question list.
func (h *Handler) BulkFinalize(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}

	var domainID *uuid.UUID
	if v, ok := formID(r, "domain_id"); ok {
		domainID = &v
	}

	res, err := h.assessments.BulkFinalize(r.Context(), id, domainID, middleware.UserIDFrom(r.Context()))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if _, err := h.reviews.RecomputeSummary(r.Context(), id); err != nil {
		h.log.Warn("could not refresh the summary after bulk sign-off", "assessment_id", id, "error", err)
	}

	view, err := h.buildResultsView(r, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view.BulkResult = &res
	w.Header().Set("HX-Trigger", "signOffChanged")
	h.renderPartial(w, r, http.StatusOK, "question_list", view)
}

// SignOffProgress renders the progress bar. It is refreshed out of band
// whenever a sign-off changes, so the header stays truthful without the page
// re-rendering every question.
func (h *Handler) SignOffProgress(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view, err := h.buildResultsView(r, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "signoff_panel", view)
}

// CloseAssessment marks the assessment complete.
func (h *Handler) CloseAssessment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.assessments.CloseAssessment(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, "/assessments/"+uuidStr(id))
}

// ReopenAssessment takes a closed assessment back into review.
func (h *Handler) ReopenAssessment(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := h.assessments.ReopenAssessment(r.Context(), id); err != nil {
		h.fail(w, r, err)
		return
	}
	h.redirect(w, r, "/assessments/"+uuidStr(id))
}

// assessmentClosed reports whether the assessment is the signed record, in
// which case every editing control is rendered inert.
func (h *Handler) assessmentClosed(r *http.Request, assessmentID uuid.UUID) (bool, error) {
	a, err := h.assessments.GetAssessment(r.Context(), assessmentID)
	if err != nil {
		return false, err
	}
	return a.Status == model.StatusClosed, nil
}

// SummaryPanel re-renders the summary out of band after a sign-off changes
// the counters.
func (h *Handler) SummaryPanel(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	view, err := h.buildResultsView(r, id)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "summary_panel", view)
}

// ExportCSV streams the reviewed assessment as a CSV download.
func (h *Handler) ExportCSV(w http.ResponseWriter, r *http.Request) {
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

	// The export is built into a buffer before any header is written. Headers
	// go out before the body, so a failure part-way through streaming could
	// not be turned into an error page - it would hand the user a truncated
	// file they might not notice was truncated.
	var buf bytes.Buffer
	// Excel on Windows needs a BOM to read UTF-8 CSV without mangling accents.
	// It is written into the buffer rather than separately so the declared
	// Content-Length covers the whole body.
	buf.Write([]byte{0xEF, 0xBB, 0xBF})
	if err := h.assessments.ExportCSV(r.Context(), id, &buf); err != nil {
		h.fail(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+assessment.ExportFilename(a)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	if _, err := w.Write(buf.Bytes()); err != nil {
		h.log.Warn("export download was interrupted", "assessment_id", id, "error", err)
	}
}

// ExportXLSX streams the reviewed assessment as an Excel download, formatted
// like the originally-uploaded questionnaire.
func (h *Handler) ExportXLSX(w http.ResponseWriter, r *http.Request) {
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

	// Built into a buffer first, same reasoning as ExportCSV above: a failure
	// part-way through must not hand the user a truncated file with a 200
	// header already sent.
	var buf bytes.Buffer
	if err := h.assessments.ExportXLSX(r.Context(), id, &buf); err != nil {
		h.fail(w, r, err)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+assessment.ExportXLSXFilename(a)+`"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	if _, err := w.Write(buf.Bytes()); err != nil {
		h.log.Warn("export download was interrupted", "assessment_id", id, "error", err)
	}
}
