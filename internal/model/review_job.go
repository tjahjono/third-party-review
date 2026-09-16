package model

import (
	"time"

	"github.com/google/uuid"
)

// ReviewJob is one background AI review run over an assessment. Table:
// review_jobs.
//
// Progress counters back the HTMX status poll so the UI can show a real
// progress bar rather than an indeterminate spinner.
type ReviewJob struct {
	ID           uuid.UUID `json:"id"`
	AssessmentID uuid.UUID `json:"assessment_id"`
	Status       JobStatus `json:"status"`
	// Scope narrows which questions this run covers; see ReviewScope.
	Scope ReviewScope `json:"scope"`

	TotalQuestions  int `json:"total_questions"`
	DoneQuestions   int `json:"done_questions"`
	FailedQuestions int `json:"failed_questions"`

	// Stage is a short human-readable description of the current step, e.g.
	// "Reviewing Cloud Security (3 of 8)".
	Stage string `json:"stage"`

	Provider string     `json:"provider"`
	Model    string     `json:"model"`
	RubricID *uuid.UUID `json:"rubric_id,omitempty"`
	// Language is snapshotted from the requesting user's account preference at
	// Enqueue time, because the worker always re-reads the job fresh from the
	// database (see Run) rather than trusting anything held in memory.
	Language Language `json:"language"`

	Error string `json:"error,omitempty"`

	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	HeartbeatAt *time.Time `json:"heartbeat_at,omitempty"`
}
