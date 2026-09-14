package domain

import (
	"strings"
	"time"
)

// Vendor is the third party being assessed.
type Vendor struct {
	ID           int64
	Name         string
	ContactName  string
	ContactEmail string
	Notes        string
	CreatedAt    time.Time
	UpdatedAt    time.Time

	// AssessmentCount is populated by list queries for display only.
	AssessmentCount int
}

// Validate checks the vendor is safe to persist.
func (v *Vendor) Validate() error {
	var errs ValidationErrors
	v.Name = strings.TrimSpace(v.Name)
	v.ContactName = strings.TrimSpace(v.ContactName)
	v.ContactEmail = strings.TrimSpace(v.ContactEmail)
	if v.Name == "" {
		errs.Add("name", "Vendor name is required.")
	} else if len(v.Name) > 200 {
		errs.Add("name", "Vendor name must be 200 characters or fewer.")
	}
	if v.ContactEmail != "" && !strings.Contains(v.ContactEmail, "@") {
		errs.Add("contact_email", "Contact email does not look like an email address.")
	}
	return errs.OrNil()
}
