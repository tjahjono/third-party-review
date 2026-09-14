package handler

import (
	"errors"
	"net/http"
	"strings"

	"third-party-review/internal/delivery/http/middleware"
	"third-party-review/internal/domain"
	"third-party-review/internal/service/assessment"
)

type resultsView struct {
	Assessment *domain.Assessment
	Questions  []*domain.Question
	Domains    []*domain.AssessmentDomain
	Summary    *domain.AssessmentSummary
	Job        *domain.ReviewJob
	Rubric     *domain.Rubric
	// Filter state, so the view keeps the user's narrowing across swaps.
	FilterDomain string
	FilterStatus string
	FlaggedOnly  bool
	// Polling is true while a review is queued or running, so a page loaded
	// mid-review keeps the progress panel updating.
	Polling bool

	// Progress is the human sign-off state shown in the header bar.
	Progress assessment.SignOffProgress
	// BulkResult is set after a bulk sign-off so the outcome can be reported.
	BulkResult *assessment.BulkFinalizeResult
	// Closed disables every editing control once the assessment is the signed
	// record.
	Closed bool
	// User is the signed-in reviewer, for attribution in the UI.
	User *domain.User
	// CSRFToken is embedded in every mutating form.
	CSRFToken string
}

// AssessmentDetail renders the assessment page: progress, summary and the
// per-question results.
func (h *Handler) AssessmentDetail(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "assessmentID")
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
	id, err := pathInt(r, "assessmentID")
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
func (h *Handler) buildResultsView(r *http.Request, id int64) (*resultsView, error) {
	ctx := r.Context()

	a, err := h.Assessments.GetAssessment(ctx, id)
	if err != nil {
		return nil, err
	}
	domains, err := h.Assessments.ListDomains(ctx)
	if err != nil {
		return nil, err
	}

	f := domain.QuestionFilter{AssessmentID: id}
	q := r.URL.Query()

	filterDomain := strings.TrimSpace(q.Get("domain"))
	if filterDomain != "" {
		if v, ok := parseInt64(filterDomain); ok {
			f.DomainID = &v
		}
	}
	filterStatus := strings.TrimSpace(q.Get("review_status"))
	if filterStatus != "" {
		s := domain.ReviewStatus(filterStatus)
		if s.Valid() {
			f.ReviewStatus = &s
		}
	}
	flaggedOnly := q.Get("flagged") == "1"
	f.FlaggedOnly = flaggedOnly

	questions, err := h.Assessments.ListQuestions(ctx, f)
	if err != nil {
		return nil, err
	}

	progress, err := h.Assessments.Progress(ctx, id)
	if err != nil {
		return nil, err
	}

	view := &resultsView{
		Assessment:   a,
		Questions:    questions,
		Domains:      domains,
		Summary:      a.Summary,
		FilterDomain: filterDomain,
		FilterStatus: filterStatus,
		FlaggedOnly:  flaggedOnly,
		Progress:     progress,
		Closed:       a.Status == domain.StatusClosed,
		User:         middleware.UserFrom(ctx),
		CSRFToken:    middleware.CSRFTokenFrom(ctx),
	}

	if job, err := h.Reviews.Status(ctx, id); err == nil {
		view.Job = job
		// If a run is still in flight, the page must resume polling on load -
		// otherwise a reload during a review leaves a frozen progress bar.
		view.Polling = !job.Status.Terminal()
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}

	if rubric, err := h.Assessments.GetRubric(ctx, id); err == nil {
		view.Rubric = rubric
	} else if !errors.Is(err, domain.ErrNotFound) {
		return nil, err
	}
	return view, nil
}

// StartReview queues a background AI review and swaps in the progress panel.
func (h *Handler) StartReview(w http.ResponseWriter, r *http.Request) {
	id, err := pathInt(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	job, err := h.Reviews.Enqueue(r.Context(), id)
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
	AssessmentID int64
	Job          *domain.ReviewJob
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
	id, err := pathInt(r, "assessmentID")
	if err != nil {
		h.fail(w, r, err)
		return
	}
	job, err := h.Reviews.Status(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			h.renderPartial(w, r, http.StatusOK, "review_status", reviewStatusView{AssessmentID: id})
			return
		}
		h.fail(w, r, err)
		return
	}

	view := reviewStatusView{
		AssessmentID: id,
		Job:          job,
		Polling:      !job.Status.Terminal(),
		Finished:     job.Status == domain.JobSucceeded,
	}
	h.renderPartial(w, r, http.StatusOK, "review_status", view)
}
