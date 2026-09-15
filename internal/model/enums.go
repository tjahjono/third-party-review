package model

// Column enum types.
//
// These are the string (and small-integer) domains the tables constrain their
// columns to. They belong with the structs because a struct field cannot be
// declared without them and because the database CHECK constraints are written
// against exactly these values.
//
// Their behaviour does not: Label, Valid, Band and friends moved to the layer
// that consumes them.

// AssessmentStatus is the lifecycle state of one review cycle.
type AssessmentStatus string

const (
	StatusUploaded  AssessmentStatus = "uploaded"
	StatusMapped    AssessmentStatus = "mapped"
	StatusReviewing AssessmentStatus = "reviewing"
	StatusReviewed  AssessmentStatus = "reviewed"
	StatusClosed    AssessmentStatus = "closed"
)

// AllAssessmentStatuses is the canonical ordering used for progress display.
var AllAssessmentStatuses = []AssessmentStatus{
	StatusUploaded, StatusMapped, StatusReviewing, StatusReviewed, StatusClosed,
}

// ReviewStatus tracks the human-in-the-loop progress of one question.
type ReviewStatus string

const (
	ReviewPending   ReviewStatus = "pending"
	ReviewAIDrafted ReviewStatus = "ai_drafted"
	ReviewFinalized ReviewStatus = "finalized"
)

// RiskScore is the per-answer residual risk on a 1-5 scale, where 1 is the
// lowest risk. The UI displays a band derived from it.
type RiskScore int

const (
	RiskMin RiskScore = 1
	RiskMax RiskScore = 5
)

// RiskBand is the coarse label shown in the UI, derived from the numeric
// score. It is not stored: it is computed on read.
type RiskBand string

const (
	BandLow      RiskBand = "low"
	BandMedium   RiskBand = "medium"
	BandHigh     RiskBand = "high"
	BandCritical RiskBand = "critical"
	BandUnknown  RiskBand = "unknown"
)

// Completeness describes whether the vendor actually answered the question.
type Completeness string

const (
	CompletenessComplete      Completeness = "complete"
	CompletenessPartial       Completeness = "partial"
	CompletenessMissing       Completeness = "missing"
	CompletenessNonResponsive Completeness = "non_responsive"
	CompletenessUnknown       Completeness = "unknown"
)

// FlagKind classifies why an answer was flagged.
type FlagKind string

const (
	FlagInconsistency  FlagKind = "inconsistency"
	FlagSecurityRisk   FlagKind = "security_risk"
	FlagRubricGap      FlagKind = "rubric_gap"
	FlagMissingAnswer  FlagKind = "missing_answer"
	FlagEvidenceAbsent FlagKind = "evidence_absent"
	FlagVague          FlagKind = "vague"
)

// JobStatus is the lifecycle of a background AI review run.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// QuestionField identifies one logical column of the TPSA questionnaire
// template. The template is a strong prior, not a guarantee, so every field
// except FieldQuestion is optional and any column may be left unmapped.
type QuestionField string

const (
	FieldQuestion           QuestionField = "question_text"
	FieldAssessorRemark     QuestionField = "assessor_remark"
	FieldThirdPartyAnswer   QuestionField = "third_party_answer"
	FieldThirdPartyRemark   QuestionField = "third_party_remark"
	FieldAssessorFeedback   QuestionField = "assessor_feedback"
	FieldThirdPartyFeedback QuestionField = "third_party_feedback"
	FieldLinkEvidence       QuestionField = "link_evidence"
)
