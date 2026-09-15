package model

import (
	"time"

	"github.com/google/uuid"
)

// Vendor is the third party being assessed. Table: vendors.
type Vendor struct {
	ID           uuid.UUID `json:"id"`
	Name         string    `json:"name"`
	ContactName  string    `json:"contact_name"`
	ContactEmail string    `json:"contact_email"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// AssessmentCount is not a column: list queries join it in for display.
	AssessmentCount int `json:"assessment_count,omitempty"`
}
