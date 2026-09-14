package domain

import (
	"strings"
	"time"
)

// AssessmentStatus is the lifecycle state of one review cycle.
type AssessmentStatus string

const (
	StatusUploaded  AssessmentStatus = "uploaded"
	StatusMapped    AssessmentStatus = "mapped"
	StatusReviewing AssessmentStatus = "reviewing"
	StatusReviewed  AssessmentStatus = "reviewed"
	StatusClosed    AssessmentStatus = "closed"
)

// AllAssessmentStatuses is the canonical ordering used for progress display.
var AllAssessmentStatuses = []AssessmentStatus{
	StatusUploaded, StatusMapped, StatusReviewing, StatusReviewed, StatusClosed,
}

// Valid reports whether s is a known status.
func (s AssessmentStatus) Valid() bool {
	for _, v := range AllAssessmentStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// Label is the human-readable form shown in the UI.
func (s AssessmentStatus) Label() string {
	switch s {
	case StatusUploaded:
		return "Uploaded"
	case StatusMapped:
		return "Mapped"
	case StatusReviewing:
		return "AI review running"
	case StatusReviewed:
		return "Reviewed"
	case StatusClosed:
		return "Closed"
	}
	return string(s)
}

// CanStartReview reports whether an AI review may be triggered from this state.
// Re-reviewing an already-reviewed assessment is allowed; ReviewResult rows are
// versioned by run so earlier results are preserved.
func (s AssessmentStatus) CanStartReview() bool {
	return s == StatusMapped || s == StatusReviewed
}

// Assessment is one review cycle tied to a Vendor.
type Assessment struct {
	ID       int64
	VendorID int64
	Title    string
	Status   AssessmentStatus

	// Source file metadata. The file itself is parsed on upload and not
	// retained beyond the ingestion step; the name and checksum are kept for
	// auditability.
	SourceFilename string
	SourceSize     int64
	SourceSHA256   string

	// ColumnMapping is the user-confirmed header mapping, persisted for audit.
	ColumnMapping *ColumnMapping

	// CurrentRunID is the most recent completed AI review run, if any.
	CurrentRunID *int64

	CreatedAt  time.Time
	UpdatedAt  time.Time
	MappedAt   *time.Time
	ReviewedAt *time.Time
	ClosedAt   *time.Time

	// Joined for display.
	VendorName string
	Summary    *AssessmentSummary
}

// Validate checks the assessment is safe to persist.
func (a *Assessment) Validate() error {
	var errs ValidationErrors
	a.Title = strings.TrimSpace(a.Title)
	if a.VendorID == 0 {
		errs.Add("vendor_id", "Select a vendor for this assessment.")
	}
	if a.Title == "" {
		errs.Add("title", "Assessment title is required.")
	} else if len(a.Title) > 200 {
		errs.Add("title", "Assessment title must be 200 characters or fewer.")
	}
	if a.Status == "" {
		a.Status = StatusUploaded
	}
	if !a.Status.Valid() {
		errs.Add("status", "Unknown assessment status "+string(a.Status)+".")
	}
	return errs.OrNil()
}

// Upload is the stored copy of the original questionnaire file. Keeping it
// lets the mapping step be revisited without a re-upload and makes an
// ingestion decision auditable against the source.
type Upload struct {
	AssessmentID int64
	Filename     string
	ContentType  string
	Content      []byte
	ByteSize     int64
	SHA256       string
	// SheetName records which worksheet the user settled on.
	SheetName  string
	UploadedAt time.Time
}
