package model

import (
	"time"

	"github.com/google/uuid"
)

// AssessmentDomain is one TPSA assessment area (Network Security, Cloud
// Security, ...). Table: assessment_domains.
//
// It is a seeded lookup table rather than a Go enum so new domains can be
// added without a schema change or redeploy.
type AssessmentDomain struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	SortOrder int       `json:"sort_order"`
	// ScrutinyNote is injected into the AI prompt to calibrate how strictly
	// answers in this domain are judged. Editable per domain in the database.
	ScrutinyNote string    `json:"scrutiny_note"`
	Active       bool      `json:"active"`
	CreatedAt    time.Time `json:"created_at"`
}
