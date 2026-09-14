package domain

import (
	"strings"
	"time"
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

// Terminal reports whether the job has finished, successfully or not.
func (s JobStatus) Terminal() bool {
	return s == JobSucceeded || s == JobFailed || s == JobCancelled
}

// Label is the human-readable form shown in the UI.
func (s JobStatus) Label() string {
	switch s {
	case JobQueued:
		return "Queued"
	case JobRunning:
		return "Running"
	case JobSucceeded:
		return "Completed"
	case JobFailed:
		return "Failed"
	case JobCancelled:
		return "Cancelled"
	}
	return string(s)
}

// ReviewJob is one background AI review run over an assessment. Progress
// counters back the HTMX status poll so the UI can show a real progress bar
// rather than an indeterminate spinner.
type ReviewJob struct {
	ID           int64
	AssessmentID int64
	Status       JobStatus

	TotalQuestions  int
	DoneQuestions   int
	FailedQuestions int

	// Stage is a short human-readable description of the current step, e.g.
	// "Reviewing Cloud Security (3 of 8)".
	Stage string

	Provider string
	Model    string
	RubricID *int64

	Error string

	CreatedAt   time.Time
	StartedAt   *time.Time
	FinishedAt  *time.Time
	HeartbeatAt *time.Time
}

// Percent reports completion as a whole number for the progress bar.
func (j *ReviewJob) Percent() int {
	if j == nil || j.TotalQuestions == 0 {
		return 0
	}
	p := int(float64(j.DoneQuestions) / float64(j.TotalQuestions) * 100)
	if p > 100 {
		return 100
	}
	return p
}

// Stalled reports whether a running job has missed its heartbeat for longer
// than the given window, which means the worker died mid-run (for example the
// process was restarted). Such jobs are reclaimed on startup.
func (j *ReviewJob) Stalled(now time.Time, window time.Duration) bool {
	if j == nil || j.Status != JobRunning {
		return false
	}
	if j.HeartbeatAt == nil {
		return j.StartedAt != nil && now.Sub(*j.StartedAt) > window
	}
	return now.Sub(*j.HeartbeatAt) > window
}

// Summary is a one-line description used in logs.
func (j *ReviewJob) Summary() string {
	if j == nil {
		return "no job"
	}
	var b strings.Builder
	b.WriteString(string(j.Status))
	b.WriteString(" ")
	b.WriteString(j.Stage)
	return strings.TrimSpace(b.String())
}
