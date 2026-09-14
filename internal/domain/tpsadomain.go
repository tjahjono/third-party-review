package domain

import "time"

// AssessmentDomain is one TPSA assessment area (Network Security, Cloud
// Security, ...). It is a seeded lookup table rather than a Go enum so new
// domains can be added without a schema change or redeploy.
type AssessmentDomain struct {
	ID        int64
	Name      string
	Slug      string
	SortOrder int
	// ScrutinyNote is injected into the AI prompt to calibrate how strictly
	// answers in this domain are judged. Editable per domain in the database.
	ScrutinyNote string
	Active       bool
	CreatedAt    time.Time
}

// SeededDomainNames lists the 8 domains inserted by the initial migration.
// It exists for tests and fixtures; production code reads the table.
var SeededDomainNames = []string{
	"Network Security",
	"Application Security",
	"Logical Access Security",
	"Data Security",
	"Security Logging and Monitoring",
	"Change, Performance and Capacity Management",
	"Cloud Security",
	"AI Security",
}
