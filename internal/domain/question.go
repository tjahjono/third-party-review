package domain

import (
	"strings"
	"time"
)

// ReviewStatus tracks the human-in-the-loop progress of one question.
type ReviewStatus string

const (
	ReviewPending   ReviewStatus = "pending"
	ReviewAIDrafted ReviewStatus = "ai_drafted"
	ReviewFinalized ReviewStatus = "finalized"
)

// Valid reports whether s is a known review status.
func (s ReviewStatus) Valid() bool {
	switch s {
	case ReviewPending, ReviewAIDrafted, ReviewFinalized:
		return true
	}
	return false
}

// Label is the human-readable form shown in the UI.
func (s ReviewStatus) Label() string {
	switch s {
	case ReviewPending:
		return "Not reviewed"
	case ReviewAIDrafted:
		return "AI draft - needs sign-off"
	case ReviewFinalized:
		return "Finalized"
	}
	return string(s)
}

// Question is a single questionnaire item belonging to exactly one domain.
type Question struct {
	ID           int64
	AssessmentID int64
	DomainID     int64
	// SourceRow is the zero-based row index in the uploaded file, kept so the
	// UI can point a user back at the original spreadsheet row.
	SourceRow int
	// Position orders questions within the assessment as they appeared.
	Position int

	QuestionText       string
	AssessorRemark     string
	ThirdPartyAnswer   string
	ThirdPartyRemark   string
	ThirdPartyFeedback string
	LinkEvidence       string

	// AssessorFeedbackDraft is written by the AI and never overwritten by the
	// finalize step, so there is always a record of what the AI said.
	AssessorFeedbackDraft string
	// AssessorFeedbackFinal is the human-signed-off text. Starts as a copy of
	// the draft and may be edited before sign-off.
	AssessorFeedbackFinal string

	ReviewStatus ReviewStatus
	FinalizedAt  *time.Time
	FinalizedBy  *int64

	CreatedAt time.Time
	UpdatedAt time.Time

	// Joined for display and for AI prompt construction.
	DomainName   string
	ScrutinyNote string
	// LatestResult is the most recent AI result for this question, if any.
	LatestResult *ReviewResult
}

// HasEvidence reports whether a supporting-evidence reference was supplied.
// Presence alone is a useful signal; the link is never fetched (v1 non-goal).
func (q *Question) HasEvidence() bool {
	return strings.TrimSpace(q.LinkEvidence) != ""
}

// AnswerIsBlank reports whether the vendor left the answer empty. This is a
// cheap pre-check done before the AI call so obviously missing answers are
// flagged even if the AI is unavailable.
func (q *Question) AnswerIsBlank() bool {
	return strings.TrimSpace(q.ThirdPartyAnswer) == ""
}

// EffectiveFeedback returns the text that exports and summaries should use:
// the finalized feedback when signed off, otherwise the AI draft. The second
// return value reports whether the text is still an unconfirmed draft.
func (q *Question) EffectiveFeedback() (text string, unconfirmed bool) {
	if q.ReviewStatus == ReviewFinalized && strings.TrimSpace(q.AssessorFeedbackFinal) != "" {
		return q.AssessorFeedbackFinal, false
	}
	if strings.TrimSpace(q.AssessorFeedbackFinal) != "" {
		return q.AssessorFeedbackFinal, true
	}
	return q.AssessorFeedbackDraft, true
}

// Validate checks the question is safe to persist.
func (q *Question) Validate() error {
	var errs ValidationErrors
	q.QuestionText = strings.TrimSpace(q.QuestionText)
	if q.AssessmentID == 0 {
		errs.Add("assessment_id", "Question is not attached to an assessment.")
	}
	if q.DomainID == 0 {
		errs.Add("domain_id", "Every question must belong to exactly one domain.")
	}
	if q.QuestionText == "" {
		errs.Add("question_text", "Question text cannot be empty.")
	}
	if q.ReviewStatus == "" {
		q.ReviewStatus = ReviewPending
	}
	if !q.ReviewStatus.Valid() {
		errs.Add("review_status", "Unknown review status "+string(q.ReviewStatus)+".")
	}
	return errs.OrNil()
}

// QuestionFilter narrows a question listing.
type QuestionFilter struct {
	AssessmentID int64
	DomainID     *int64
	ReviewStatus *ReviewStatus
	// FlaggedOnly restricts results to questions whose latest AI result
	// carries at least one flag.
	FlaggedOnly bool
	// MinRiskScore, when set, restricts results to questions scoring at or
	// above the given 1-5 value.
	MinRiskScore *int
}

// BulkFinalizeResult reports what a bulk sign-off actually did.
type BulkFinalizeResult struct {
	Finalized int
	// Skipped counts questions passed over because they had no draft to sign
	// off. Accepting a blank draft would put an empty finding into the record,
	// which reads as reviewed.
	Skipped int
	// AlreadyFinal counts questions a human had already signed.
	AlreadyFinal int
}

// SignOffProgress is the reviewer-facing state of an assessment's sign-off.
type SignOffProgress struct {
	Total     int
	Finalized int
	Pending   int
	// NoDraft counts pending questions the AI left without a draft, which have
	// to be written by hand before the assessment can be closed.
	NoDraft      int
	Percent      int
	ReadyToClose bool
}
