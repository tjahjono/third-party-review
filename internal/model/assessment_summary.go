package model

import (
	"time"

	"github.com/google/uuid"
)

// DomainScore is the aggregated risk for one domain within an assessment.
// Stored as a JSONB array on the summary row: it is derived data with no
// identity of its own and is always rewritten wholesale.
type DomainScore struct {
	DomainID        uuid.UUID `json:"domain_id"`
	DomainName      string    `json:"domain_name"`
	SortOrder       int       `json:"sort_order"`
	QuestionCount   int       `json:"question_count"`
	ScoredCount     int       `json:"scored_count"`
	MeanScore       float64   `json:"mean_score"`
	WorstScore      RiskScore `json:"worst_score"`
	WeightedScore   float64   `json:"weighted_score"`
	FlaggedCount    int       `json:"flagged_count"`
	IncompleteCount int       `json:"incomplete_count"`
}

// AssessmentSummary is the aggregated result for a whole assessment. Table:
// assessment_summaries, one row per assessment.
type AssessmentSummary struct {
	AssessmentID uuid.UUID  `json:"assessment_id"`
	RunID        *uuid.UUID `json:"run_id,omitempty"`

	QuestionCount int `json:"question_count"`
	ScoredCount   int `json:"scored_count"`
	// OverallScore is the weighted worst-case aggregate on the 1-5 scale.
	OverallScore float64   `json:"overall_score"`
	MeanScore    float64   `json:"mean_score"`
	WorstScore   RiskScore `json:"worst_score"`

	FlaggedCount        int `json:"flagged_count"`
	IncompleteCount     int `json:"incomplete_count"`
	PendingFinalization int `json:"pending_finalization"`
	FinalizedCount      int `json:"finalized_count"`

	DomainScores []DomainScore `json:"domain_scores"`
	// Narrative is the AI's high-level summary of the assessment.
	Narrative string `json:"narrative"`

	GeneratedAt time.Time `json:"generated_at"`
}
