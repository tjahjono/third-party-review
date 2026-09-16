package handler

import (
	"errors"
	"net/http"
	"strings"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"

	"github.com/google/uuid"
)

type resultsView struct {
	Assessment *model.Assessment
	Questions  []*model.Question
	Domains    []*model.AssessmentDomain
	Summary    *model.AssessmentSummary
	Job        *model.ReviewJob
	Rubric     *model.Rubric
	// Filter state, so the view keeps the user's narrowing across swaps.
	FilterDomain  string
	FilterStatus  string
	FilterConcern string
	// Polling is true while a review is queued or running, so a page loaded
	// mid-review keeps the progress panel updating.
	Polling bool

	// Progress is the human sign-off state shown in the header bar.
	Progress dto.SignOffProgress
	// BulkResult is set after a bulk sign-off so the outcome can be reported.
	BulkResult *dto.BulkFinalizeResult
	// Closed disables every editing control once the assessment is the signed
	// record.
	Closed bool
	// User is the signed-in reviewer, for attribution in the UI.
	User *model.User
	// CSRFToken is embedded in every mutating form.
	CSRFToken string
}

// AssessmentDetail renders the assessment page: progress, summary and the
// per-question results.
func (h *Handler) AssessmentDetail(w http.ResponseWriter, r *http.Request) {
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
	h.renderPage(w, r, http.StatusOK, "assessment", pageData{
		Title:   view.Assessment.Title,
		Active:  "assessments",
		Domains: view.Domains,
		Data:    view,
	})
}

// QuestionList swaps just the filtered question list.
func (h *Handler) QuestionList(w http.ResponseWriter, r *http.Request) {
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
	h.renderPartial(w, r, http.StatusOK, "question_list", view)
}

// buildResultsView assembles everything the assessment page needs.
func (h *Handler) buildResultsView(r *http.Request, id uuid.UUID) (*resultsView, error) {
	ctx := r.Context()

	a, err := h.assessments.GetAssessment(ctx, id)
	if err != nil {
		return nil, err
	}
	domains, err := h.assessments.ListDomains(ctx)
	if err != nil {
		return nil, err
	}

	f := dto.QuestionFilter{AssessmentID: id}
	q := r.URL.Query()

	filterDomain := strings.TrimSpace(q.Get("domain"))
	if filterDomain != "" {
		if v, ok := parseUUID(filterDomain); ok {
			f.DomainID = &v
		}
	}
	filterStatus := strings.TrimSpace(q.Get("review_status"))
	if filterStatus != "" {
		s := model.ReviewStatus(filterStatus)
		if helper.ValidReviewStatus(s) {
			f.ReviewStatus = &s
		}
	}
	// concern narrows by what the AI made of the answer: "flagged" for
	// anything that raised a concern, "clean" for its complement - answers
	// the AI reviewed and found nothing wrong with. Anything else (including
	// not yet reviewed) is left out of both.
	filterConcern := strings.TrimSpace(q.Get("concern"))
	switch filterConcern {
	case "flagged":
		f.FlaggedOnly = true
	case "clean":
		f.NoConcernOnly = true
	default:
		filterConcern = ""
	}

	questions, err := h.assessments.ListQuestions(ctx, f)
	if err != nil {
		return nil, err
	}

	progress, err := h.assessments.Progress(ctx, id)
	if err != nil {
		return nil, err
	}

	view := &resultsView{
		Assessment:    a,
		Questions:     questions,
		Domains:       domains,
		Summary:       a.Summary,
		FilterDomain:  filterDomain,
		FilterStatus:  filterStatus,
		FilterConcern: filterConcern,
		Progress:      progress,
		Closed:        a.Status == model.StatusClosed,
		User:          middleware.UserFrom(ctx),
		CSRFToken:     middleware.CSRFTokenFrom(ctx),
	}

	if job, err := h.reviews.Status(ctx, id); err == nil {
		view.Job = job
		// If a run is still in flight, the page must resume polling on load -
		// otherwise a reload during a review leaves a frozen progress bar.
		view.Polling = !helper.TerminalJob(job.Status)
	} else if !errors.Is(err, helper.ErrNotFound) {
		return nil, err
	}

	if rubric, err := h.assessments.GetRubric(ctx, id); err == nil {
		view.Rubric = rubric
	} else if !errors.Is(err, helper.ErrNotFound) {
		return nil, err
	}
	return view, nil
}

// StartReview queues a background AI review and swaps in the progress panel.
// The form may narrow the run with scope=unreviewed (only questions with no
// AI pass yet) or scope=selected plus one or more question_ids (a
// reviewer-chosen subset); an absent or empty scope reviews everything, as
// before.
func (h *Handler) StartReview(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.fail(w, r, helper.ValidationError{Field: "form", Message: "The form could not be read."})
		return
	}
	scope := model.ReviewScope(strings.TrimSpace(r.FormValue("scope")))

	var questionIDs []uuid.UUID
	if scope == model.ScopeSelected {
		for _, raw := range r.Form["question_ids"] {
			if v, ok := parseUUID(raw); ok {
				questionIDs = append(questionIDs, v)
			}
		}
	}

	language := model.LanguageEnglish
	if u := middleware.UserFrom(r.Context()); u != nil && u.Language != "" {
		language = u.Language
	}
	job, err := h.reviews.Enqueue(r.Context(), id, scope, questionIDs, language)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.renderPartial(w, r, http.StatusOK, "review_status", reviewStatusView{
		AssessmentID: id,
		Job:          job,
		Polling:      true,
	})
}

type reviewStatusView struct {
	AssessmentID uuid.UUID
	Job          *model.ReviewJob
	// Polling tells the template to keep the HTMX poll trigger attached. It is
	// switched off on a terminal status so the browser stops polling instead
	// of hammering the endpoint forever.
	Polling bool
	// Finished tells the page to reload so the new results are shown.
	Finished bool
}

// ReviewStatus is the HTMX poll target. It returns the progress panel and,
// once the run finishes, stops the poll and asks the page to refresh.
func (h *Handler) ReviewStatus(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	job, err := h.reviews.Status(r.Context(), id)
	if err != nil {
		if errors.Is(err, helper.ErrNotFound) {
			h.renderPartial(w, r, http.StatusOK, "review_status", reviewStatusView{AssessmentID: id})
			return
		}
		h.fail(w, r, err)
		return
	}

	view := reviewStatusView{
		AssessmentID: id,
		Job:          job,
		Polling:      !helper.TerminalJob(job.Status),
		Finished:     job.Status == model.JobSucceeded,
	}
	h.renderPartial(w, r, http.StatusOK, "review_status", view)
}
