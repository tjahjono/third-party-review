package model

import (
	"time"

	"github.com/google/uuid"
)

// Assessment is one review cycle tied to a Vendor. Table: assessments.
type Assessment struct {
	ID       uuid.UUID        `json:"id"`
	VendorID uuid.UUID        `json:"vendor_id"`
	Title    string           `json:"title"`
	Status   AssessmentStatus `json:"status"`

	// Source file metadata. The name and checksum are kept for auditability
	// even after the stored bytes are discarded.
	SourceFilename string `json:"source_filename"`
	SourceSize     int64  `json:"source_size"`
	SourceSHA256   string `json:"source_sha256"`

	// ColumnMapping is the user-confirmed header mapping, persisted as JSONB.
	ColumnMapping *ColumnMapping `json:"column_mapping,omitempty"`

	// CurrentRunID is the most recent completed AI review run, if any.
	CurrentRunID *uuid.UUID `json:"current_run_id,omitempty"`

	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	MappedAt   *time.Time `json:"mapped_at,omitempty"`
	ReviewedAt *time.Time `json:"reviewed_at,omitempty"`
	ClosedAt   *time.Time `json:"closed_at,omitempty"`

	// Not columns: joined in for display.
	VendorName string             `json:"vendor_name,omitempty"`
	Summary    *AssessmentSummary `json:"summary,omitempty"`
}

// Upload is the stored copy of the original questionnaire file. Table:
// assessment_uploads, one row per assessment.
//
// Keeping it lets the mapping step be revisited without a re-upload and makes
// an ingestion decision auditable against the source.
type Upload struct {
	AssessmentID uuid.UUID `json:"assessment_id"`
	Filename     string    `json:"filename"`
	ContentType  string    `json:"content_type"`
	Content      []byte    `json:"-"`
	ByteSize     int64     `json:"byte_size"`
	SHA256       string    `json:"sha256"`
	// SheetName records which worksheet the user settled on.
	SheetName  string    `json:"sheet_name"`
	UploadedAt time.Time `json:"uploaded_at"`
}
