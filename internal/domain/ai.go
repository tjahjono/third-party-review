package domain

import "context"

// AIReviewer is the provider-agnostic contract for AI-assisted review. Every
// concrete client (Open WebUI / Qwen, OpenAI, Anthropic, mock) implements this,
// so the service layer never learns which provider is configured.
type AIReviewer interface {
	// ReviewBatch evaluates a group of answers together, which lets the model
	// spot contradictions between answers in the same assessment. Results are
	// returned keyed by QuestionID; a missing key means the model did not
	// return a usable result for that question and it should be retried
	// individually.
	ReviewBatch(ctx context.Context, req BatchReviewRequest) (BatchReviewResponse, error)

	// ReviewAnswer evaluates one answer on its own. Used as a follow-up for
	// questions the batch pass skipped or scored with low confidence.
	ReviewAnswer(ctx context.Context, req ReviewRequest) (ReviewResult, error)

	// Summarize writes the assessment-level narrative from the per-question
	// findings. Aggregated numbers are computed in Go, not by the model.
	Summarize(ctx context.Context, req SummaryRequest) (string, error)

	// Name identifies the provider for logging and for the Provider column on
	// persisted results.
	Name() string
}

// QuestionContext is the neutral, provider-independent view of one question
// handed to the AI. It deliberately carries only what the model needs.
type QuestionContext struct {
	QuestionID   int64  `json:"question_id"`
	QuestionText string `json:"question"`
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

// ReviewRequest asks for an evaluation of a single answer.
type ReviewRequest struct {
	Question QuestionContext
	// RubricExcerpt is the attached policy text, already truncated to fit.
	RubricExcerpt string
	// PeerAnswers gives brief context from other answers in the assessment so
	// a single-question follow-up can still spot contradictions.
	PeerAnswers []PeerAnswer
}

// PeerAnswer is a compact reference to another answer in the same assessment,
// used for cross-answer consistency checking.
type PeerAnswer struct {
	QuestionID int64  `json:"question_id"`
	Domain     string `json:"domain"`
	Question   string `json:"question"`
	Answer     string `json:"answer"`
}

// BatchReviewRequest asks for evaluation of several answers in one call.
type BatchReviewRequest struct {
	AssessmentTitle string
	VendorName      string
	Questions       []QuestionContext
	RubricExcerpt   string
	PeerAnswers     []PeerAnswer
}

// BatchReviewResponse carries per-question results keyed by QuestionID plus
// any cross-cutting observations the model made about the batch as a whole.
type BatchReviewResponse struct {
	Results map[int64]ReviewResult
	// Notes are batch-level observations (typically inconsistencies spanning
	// several answers) surfaced to the reviewer alongside the per-question
	// findings.
	Notes []string
	Raw   string
}

// SummaryFinding is one per-question outcome fed into the narrative request.
type SummaryFinding struct {
	Domain       string       `json:"domain"`
	Question     string       `json:"question"`
	RiskScore    RiskScore    `json:"risk_score"`
	Completeness Completeness `json:"completeness"`
	Flags        []string     `json:"flags,omitempty"`
}

// SummaryRequest asks for the assessment-level narrative.
type SummaryRequest struct {
	VendorName      string
	AssessmentTitle string
	OverallScore    float64
	FlaggedCount    int
	IncompleteCount int
	QuestionCount   int
	DomainScores    []DomainScore
	// TopFindings is the highest-risk subset, not every question, so the
	// narrative request stays inside the model's context window.
	TopFindings []SummaryFinding
	BatchNotes  []string
}
