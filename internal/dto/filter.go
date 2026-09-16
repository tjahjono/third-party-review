package dto

import (
	"github.com/google/uuid"

	"third-party-review/internal/model"
)

// AssessmentFilter narrows an assessment listing.
type AssessmentFilter struct {
	VendorID *uuid.UUID              `json:"vendor_id,omitempty"`
	Status   *model.AssessmentStatus `json:"status,omitempty"`
	Search   string                  `json:"search,omitempty"`
	Limit    int                     `json:"limit,omitempty"`
	Offset   int                     `json:"offset,omitempty"`
}

// QuestionFilter narrows a question listing.
type QuestionFilter struct {
	AssessmentID uuid.UUID           `json:"assessment_id"`
	DomainID     *uuid.UUID          `json:"domain_id,omitempty"`
	ReviewStatus *model.ReviewStatus `json:"review_status,omitempty"`
	// FlaggedOnly restricts results to questions whose latest AI result is
	// flagged - the same definition Aggregate uses for
	// AssessmentSummary.FlaggedCount: at least one flag, or a risk score at or
	// above model.RiskFlagThreshold. A question with no result yet never
	// matches; it is neither flagged nor clean, just not yet reviewed.
	FlaggedOnly bool `json:"flagged_only,omitempty"`
	// NoConcernOnly is FlaggedOnly's complement: it restricts results to
	// questions whose latest AI result is clean - a validly scored answer
	// below model.RiskFlagThreshold with no flags. Mutually exclusive with
	// FlaggedOnly; the delivery layer never sets both.
	NoConcernOnly bool `json:"no_concern_only,omitempty"`
	// MinRiskScore, when set, restricts results to questions scoring at or
	// above the given 1-5 value.
	MinRiskScore *int `json:"min_risk_score,omitempty"`
}
