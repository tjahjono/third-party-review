package helper

import (
	"fmt"

	"third-party-review/internal/model"
)

// Human-readable names for the column enums.
//
// These sit here rather than in the delivery layer because the CSV export
// writes the same words the screen shows, and a second copy in the exporter
// is how a "High" in the UI becomes a "high" in the file someone signs off.
// The handler registers them as template functions; nothing else formats an
// enum by hand.

// AssessmentStatusLabel is the human-readable assessment status.
func AssessmentStatusLabel(s model.AssessmentStatus) string {
	switch s {
	case model.StatusUploaded:
		return "Uploaded"
	case model.StatusMapped:
		return "Mapped"
	case model.StatusReviewing:
		return "AI review running"
	case model.StatusReviewed:
		return "Reviewed"
	case model.StatusClosed:
		return "Closed"
	}
	return string(s)
}

// ReviewStatusLabel is the human-readable per-question review status.
func ReviewStatusLabel(s model.ReviewStatus) string {
	switch s {
	case model.ReviewPending:
		return "Not reviewed"
	case model.ReviewAIDrafted:
		return "AI draft - needs sign-off"
	case model.ReviewFinalized:
		return "Finalized"
	}
	return string(s)
}

// RiskBandLabel is the human-readable band name.
func RiskBandLabel(b model.RiskBand) string {
	switch b {
	case model.BandLow:
		return "Low"
	case model.BandMedium:
		return "Medium"
	case model.BandHigh:
		return "High"
	case model.BandCritical:
		return "Critical"
	}
	return "Not scored"
}

// CompletenessLabel is the human-readable completeness value.
func CompletenessLabel(c model.Completeness) string {
	switch c {
	case model.CompletenessComplete:
		return "Complete"
	case model.CompletenessPartial:
		return "Partial"
	case model.CompletenessMissing:
		return "Missing"
	case model.CompletenessNonResponsive:
		return "Not responsive"
	}
	return "Unknown"
}

// FlagKindLabel is the human-readable reason an answer was flagged.
func FlagKindLabel(f model.FlagKind) string {
	switch f {
	case model.FlagInconsistency:
		return "Inconsistent with another answer"
	case model.FlagSecurityRisk:
		return "Security concern"
	case model.FlagRubricGap:
		return "Gap against rubric"
	case model.FlagMissingAnswer:
		return "No answer provided"
	case model.FlagEvidenceAbsent:
		return "No supporting evidence"
	case model.FlagVague:
		return "Vague or unverifiable"
	}
	return string(f)
}

// JobStatusLabel is the human-readable background-job status.
func JobStatusLabel(s model.JobStatus) string {
	switch s {
	case model.JobQueued:
		return "Queued"
	case model.JobRunning:
		return "Running"
	case model.JobSucceeded:
		return "Completed"
	case model.JobFailed:
		return "Failed"
	case model.JobCancelled:
		return "Cancelled"
	}
	return string(s)
}

// SummaryLine renders the overall score for logs and CSV exports.
func SummaryLine(s *model.AssessmentSummary) string {
	if s == nil {
		return "no summary"
	}
	return fmt.Sprintf("%.2f/5 (%s), %d flagged, %d incomplete, %d/%d finalized",
		s.OverallScore, RiskBandLabel(SummaryBand(s)), s.FlaggedCount, s.IncompleteCount,
		s.FinalizedCount, s.QuestionCount)
}
