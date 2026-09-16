package dto

import (
	"github.com/google/uuid"

	"third-party-review/internal/model"
)

// The provider-neutral request and response envelopes for AI review. Each
// concrete client adapts these to its own API shape, so nothing above the
// client package learns which provider is configured.

// QuestionContext is the neutral view of one question handed to the AI. It
// deliberately carries only what the model needs.
type QuestionContext struct {
	QuestionID   uuid.UUID `json:"question_id"`
	QuestionText string    `json:"question"`
	// DomainName calibrates scrutiny; AI Security and Cloud Security warrant
	// different evaluation criteria than Network Security.
	DomainName       string `json:"domain"`
	ScrutinyNote     string `json:"domain_guidance,omitempty"`
	ThirdPartyAnswer string `json:"third_party_answer"`
	ThirdPartyRemark string `json:"third_party_remark,omitempty"`
	AssessorRemark   string `json:"assessor_remark,omitempty"`
	// HasEvidence reports whether a Link Evidence reference was supplied. The
	// link itself is never fetched (v1 non-goal); its presence or absence is
	// the signal.
	HasEvidence bool `json:"has_evidence"`
}

// PeerAnswer is a compact reference to another answer in the same assessment,
// used for cross-answer consistency checking.
type PeerAnswer struct {
	QuestionID uuid.UUID `json:"question_id"`
	Domain     string    `json:"domain"`
	Question   string    `json:"question"`
	Answer     string    `json:"answer"`
}

// ReviewRequest asks for an evaluation of a single answer.
type ReviewRequest struct {
	Question QuestionContext `json:"question"`
	// RubricExcerpt is the attached policy text, already truncated to fit.
	RubricExcerpt string `json:"rubric_excerpt,omitempty"`
	// PeerAnswers gives brief context from other answers in the assessment so
	// a single-question follow-up can still spot contradictions.
	PeerAnswers []PeerAnswer `json:"peer_answers,omitempty"`
	// Language is the reviewer's preferred language for the drafted prose.
	// Empty (or "en") means English, the prompt's native language, and adds no
	// extra instruction.
	Language model.Language `json:"language,omitempty"`
}

// BatchReviewRequest asks for evaluation of several answers in one call.
type BatchReviewRequest struct {
	AssessmentTitle string            `json:"assessment_title"`
	VendorName      string            `json:"vendor_name"`
	Questions       []QuestionContext `json:"questions"`
	RubricExcerpt   string            `json:"rubric_excerpt,omitempty"`
	PeerAnswers     []PeerAnswer      `json:"peer_answers,omitempty"`
	// Language is the reviewer's preferred language for the drafted prose.
	Language model.Language `json:"language,omitempty"`
}

// BatchReviewResponse carries per-question results keyed by QuestionID plus
// any cross-cutting observations the model made about the batch as a whole.
type BatchReviewResponse struct {
	Results map[uuid.UUID]model.ReviewResult `json:"results"`
	// Notes are batch-level observations (typically inconsistencies spanning
	// several answers) surfaced to the reviewer alongside the per-question
	// findings.
	Notes []string `json:"notes,omitempty"`
	Raw   string   `json:"-"`
}

// SummaryFinding is one per-question outcome fed into the narrative request.
type SummaryFinding struct {
	Domain       string             `json:"domain"`
	Question     string             `json:"question"`
	RiskScore    model.RiskScore    `json:"risk_score"`
	Completeness model.Completeness `json:"completeness"`
	Flags        []string           `json:"flags,omitempty"`
}

// SummaryRequest asks for the assessment-level narrative.
type SummaryRequest struct {
	VendorName      string              `json:"vendor_name"`
	AssessmentTitle string              `json:"assessment_title"`
	OverallScore    float64             `json:"overall_score"`
	FlaggedCount    int                 `json:"flagged_count"`
	IncompleteCount int                 `json:"incomplete_count"`
	QuestionCount   int                 `json:"question_count"`
	DomainScores    []model.DomainScore `json:"domain_scores"`
	// TopFindings is the highest-risk subset, not every question, so the
	// narrative request stays inside the model's context window.
	TopFindings []SummaryFinding `json:"top_findings"`
	BatchNotes  []string         `json:"batch_notes,omitempty"`
	// Language is the reviewer's preferred language for the narrative.
	Language model.Language `json:"language,omitempty"`
}
