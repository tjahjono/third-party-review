package model

import (
	"time"

	"github.com/google/uuid"
)

// Question is a single questionnaire item belonging to exactly one domain.
// Table: questions.
type Question struct {
	ID           uuid.UUID `json:"id"`
	AssessmentID uuid.UUID `json:"assessment_id"`
	DomainID     uuid.UUID `json:"domain_id"`
	// SourceRow is the zero-based row index in the uploaded file, kept so the
	// UI can point a user back at the original spreadsheet row.
	SourceRow int `json:"source_row"`
	// Position orders questions within the assessment as they appeared.
	Position int `json:"position"`

	QuestionText       string `json:"question_text"`
	AssessorRemark     string `json:"assessor_remark"`
	ThirdPartyAnswer   string `json:"third_party_answer"`
	ThirdPartyRemark   string `json:"third_party_remark"`
	ThirdPartyFeedback string `json:"third_party_feedback"`
	LinkEvidence       string `json:"link_evidence"`

	// AssessorFeedbackDraft is written by the AI and never overwritten by the
	// finalize step, so there is always a record of what the AI said.
	AssessorFeedbackDraft string `json:"assessor_feedback_draft"`
	// AssessorFeedbackFinal is the human-signed-off text. Starts as a copy of
	// the draft and may be edited before sign-off.
	AssessorFeedbackFinal string `json:"assessor_feedback_final"`

	ReviewStatus ReviewStatus `json:"review_status"`
	FinalizedAt  *time.Time   `json:"finalized_at,omitempty"`
	FinalizedBy  *uuid.UUID   `json:"finalized_by,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Not columns: joined in for display and for AI prompt construction.
	DomainName   string `json:"domain_name,omitempty"`
	ScrutinyNote string `json:"scrutiny_note,omitempty"`
	// LatestResult is the most recent AI result for this question, if any.
	LatestResult *ReviewResult `json:"latest_result,omitempty"`
}
