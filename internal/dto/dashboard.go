package dto

import (
	"github.com/google/uuid"

	"third-party-review/internal/model"
)

// ActiveReview is one in-flight (queued or running) review job, joined with
// enough assessment/vendor context to show on the dashboard without a
// separate lookup per row.
type ActiveReview struct {
	Job             *model.ReviewJob
	AssessmentID    uuid.UUID
	AssessmentTitle string
	VendorName      string
}

// Dashboard is the aggregated cross-assessment view shown on the home page.
type Dashboard struct {
	VendorCount     int
	AssessmentCount int
	// StatusCounts holds every status in model.AllAssessmentStatuses, even
	// ones with zero assessments, so the template never has to guard a
	// missing key.
	StatusCounts map[model.AssessmentStatus]int

	// ReviewedCount is how many assessments have been reviewed at least once
	// (i.e. carry a summary). RiskBandCounts buckets exactly those.
	ReviewedCount  int
	RiskBandCounts map[model.RiskBand]int

	// PendingFinalization sums each reviewed assessment's count of
	// AI-drafted questions still awaiting a human sign-off.
	PendingFinalization int

	// RecentAssessments is the newest handful, most-recent first.
	RecentAssessments []*model.Assessment
	// NeedsAttention is the reviewed assessments with the most flags and the
	// highest risk, most urgent first.
	NeedsAttention []*model.Assessment
	// ActiveReviews is every review job currently queued or running.
	ActiveReviews []ActiveReview
}
