package model

import (
	"time"

	"github.com/google/uuid"
)

// Flag is a single reviewer-visible concern raised against an answer. Flags
// are stored as a JSONB array on the review_results row rather than as their
// own table: they are only ever read back with their parent result.
type Flag struct {
	Kind     FlagKind  `json:"kind"`
	Detail   string    `json:"detail"`
	Severity RiskScore `json:"severity,omitempty"`
	// RelatedQuestionIDs points at the other answers an inconsistency flag
	// contradicts, when the AI identified them.
	RelatedQuestionIDs []uuid.UUID `json:"related_question_ids,omitempty"`
}

// ReviewResult is one AI evaluation of one question. Table: review_results.
//
// Results are kept as child rows keyed by RunID rather than overwriting the
// Question, so re-reviews preserve history.
type ReviewResult struct {
	ID         uuid.UUID `json:"id"`
	QuestionID uuid.UUID `json:"question_id"`
	RunID      uuid.UUID `json:"run_id"`

	RiskScore    RiskScore    `json:"risk_score"`
	Completeness Completeness `json:"completeness"`
	Flags        []Flag       `json:"flags"`
	Rationale    string       `json:"rationale"`
	// FeedbackDraft is the prose written in the voice of a security assessor.
	FeedbackDraft string `json:"feedback_draft"`
	// Confidence is the model's self-reported confidence in [0,1]. Low values
	// drive the per-question follow-up pass.
	Confidence float64 `json:"confidence"`

	Provider string `json:"provider"`
	Model    string `json:"model"`
	// Raw is the unparsed model response, retained for debugging a bad review.
	Raw       string    `json:"raw,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}
