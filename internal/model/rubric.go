package model

import (
	"time"

	"github.com/google/uuid"
)

// Rubric is a policy or checklist the AI compares answers against. Table:
// rubrics. Its attachment to an assessment is the assessment_rubrics row.
type Rubric struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Content is free text: a pasted policy, a checklist, or structured
	// criteria. It is passed to the AI verbatim (truncated to fit context).
	Content string `json:"content"`
	// Reusable rubrics appear in the picker on every assessment.
	Reusable  bool      `json:"reusable"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AssessmentRubric attaches one rubric to one assessment. Table:
// assessment_rubrics.
type AssessmentRubric struct {
	AssessmentID uuid.UUID `json:"assessment_id"`
	RubricID     uuid.UUID `json:"rubric_id"`
	AttachedAt   time.Time `json:"attached_at"`
}
