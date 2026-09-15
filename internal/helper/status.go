package helper

import (
	"strings"
	"time"

	"third-party-review/internal/model"
)

// ---------------------------------------------------------------------------
// Assessment status
// ---------------------------------------------------------------------------

// ValidAssessmentStatus reports whether s is a known status.
func ValidAssessmentStatus(s model.AssessmentStatus) bool {
	for _, v := range model.AllAssessmentStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// CanStartReview reports whether an AI review may be triggered from this
// state. Re-reviewing an already-reviewed assessment is allowed; ReviewResult
// rows are versioned by run so earlier results are preserved.
func CanStartReview(s model.AssessmentStatus) bool {
	return s == model.StatusMapped || s == model.StatusReviewed
}

// ---------------------------------------------------------------------------
// Review status
// ---------------------------------------------------------------------------

// ValidReviewStatus reports whether s is a known review status.
func ValidReviewStatus(s model.ReviewStatus) bool {
	switch s {
	case model.ReviewPending, model.ReviewAIDrafted, model.ReviewFinalized:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Jobs
// ---------------------------------------------------------------------------

// TerminalJob reports whether the job has finished, successfully or not.
func TerminalJob(s model.JobStatus) bool {
	return s == model.JobSucceeded || s == model.JobFailed || s == model.JobCancelled
}

// JobPercent reports completion as a whole number for the progress bar.
func JobPercent(j *model.ReviewJob) int {
	if j == nil || j.TotalQuestions == 0 {
		return 0
	}
	p := int(float64(j.DoneQuestions) / float64(j.TotalQuestions) * 100)
	if p > 100 {
		return 100
	}
	return p
}

// JobStalled reports whether a running job has missed its heartbeat for longer
// than the given window, which means the worker died mid-run (for example the
// process was restarted). Such jobs are reclaimed on startup.
func JobStalled(j *model.ReviewJob, now time.Time, window time.Duration) bool {
	if j == nil || j.Status != model.JobRunning {
		return false
	}
	if j.HeartbeatAt == nil {
		return j.StartedAt != nil && now.Sub(*j.StartedAt) > window
	}
	return now.Sub(*j.HeartbeatAt) > window
}

// JobSummary is a one-line description used in logs.
func JobSummary(j *model.ReviewJob) string {
	if j == nil {
		return "no job"
	}
	return strings.TrimSpace(string(j.Status) + " " + j.Stage)
}

// ---------------------------------------------------------------------------
// Sessions
// ---------------------------------------------------------------------------

// SessionActive reports whether the session may be used to authorise a
// request.
func SessionActive(s *model.Session, now time.Time) bool {
	return s != nil && !s.MFAPending && now.Before(s.ExpiresAt)
}
