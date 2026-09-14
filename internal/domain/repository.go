package domain

import (
	"context"
	"time"
)

// Tx is an open database transaction. The service layer passes it back into
// repository calls to group writes; it never learns what backs it.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// TxManager starts transactions. RunInTx handles commit/rollback so callers
// cannot leak a transaction by returning early.
type TxManager interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// VendorRepository persists vendors.
type VendorRepository interface {
	Create(ctx context.Context, v *Vendor) error
	Update(ctx context.Context, v *Vendor) error
	GetByID(ctx context.Context, id int64) (*Vendor, error)
	List(ctx context.Context, search string, limit, offset int) ([]*Vendor, error)
	Count(ctx context.Context, search string) (int, error)
	Delete(ctx context.Context, id int64) error
}

// DomainRepository reads and writes the seeded TPSA domain lookup table.
type DomainRepository interface {
	List(ctx context.Context, includeInactive bool) ([]*AssessmentDomain, error)
	GetByID(ctx context.Context, id int64) (*AssessmentDomain, error)
	GetBySlug(ctx context.Context, slug string) (*AssessmentDomain, error)
	Create(ctx context.Context, d *AssessmentDomain) error
	Update(ctx context.Context, d *AssessmentDomain) error
}

// AssessmentRepository persists assessments.
type AssessmentRepository interface {
	Create(ctx context.Context, a *Assessment) error
	Update(ctx context.Context, a *Assessment) error
	GetByID(ctx context.Context, id int64) (*Assessment, error)
	List(ctx context.Context, f AssessmentFilter) ([]*Assessment, error)
	Count(ctx context.Context, f AssessmentFilter) (int, error)
	SetStatus(ctx context.Context, id int64, status AssessmentStatus, at time.Time) error
	SaveColumnMapping(ctx context.Context, id int64, m *ColumnMapping) error
	SetCurrentRun(ctx context.Context, id int64, runID int64) error
	Delete(ctx context.Context, id int64) error

	// SaveUpload stores the original questionnaire file.
	SaveUpload(ctx context.Context, u *Upload) error
	// GetUpload returns the stored file, or ErrNotFound if it was discarded.
	GetUpload(ctx context.Context, assessmentID int64) (*Upload, error)
	// SetUploadSheet records which worksheet the user selected.
	SetUploadSheet(ctx context.Context, assessmentID int64, sheet string) error
	// DiscardUpload drops the stored bytes once they are no longer needed.
	DiscardUpload(ctx context.Context, assessmentID int64) error
}

// AssessmentFilter narrows an assessment listing.
type AssessmentFilter struct {
	VendorID *int64
	Status   *AssessmentStatus
	Search   string
	Limit    int
	Offset   int
}

// QuestionRepository persists questionnaire items.
type QuestionRepository interface {
	// BulkCreate inserts all questions for an assessment in one round trip.
	// Assigned IDs are written back onto the supplied slice.
	BulkCreate(ctx context.Context, questions []*Question) error
	Update(ctx context.Context, q *Question) error
	GetByID(ctx context.Context, id int64) (*Question, error)
	List(ctx context.Context, f QuestionFilter) ([]*Question, error)
	// ListForReview returns every question of an assessment with the domain
	// name and scrutiny note joined, ready for prompt construction.
	ListForReview(ctx context.Context, assessmentID int64) ([]*Question, error)
	CountByAssessment(ctx context.Context, assessmentID int64) (int, error)
	// SetDomain reassigns a question, used when a detected domain boundary
	// was wrong.
	SetDomain(ctx context.Context, questionID, domainID int64) error
	// ApplyDraft copies an AI draft onto the question and moves it to
	// ai_drafted, without touching any existing finalized text.
	ApplyDraft(ctx context.Context, questionID int64, draft string) error
	// Finalize records the human-signed-off feedback. The draft column is
	// never overwritten.
	Finalize(ctx context.Context, questionID int64, final string, userID *int64, at time.Time) error
	// Unfinalize reopens a question for further editing.
	Unfinalize(ctx context.Context, questionID int64) error
	DeleteByAssessment(ctx context.Context, assessmentID int64) error
}

// ReviewResultRepository persists per-question AI results, keyed by run so
// re-reviews preserve history.
type ReviewResultRepository interface {
	BulkCreate(ctx context.Context, results []*ReviewResult) error
	// LatestByAssessment returns the newest result per question for the given
	// run, keyed by question ID. A nil runID means "the newest of any run".
	LatestByAssessment(ctx context.Context, assessmentID int64, runID *int64) (map[int64]*ReviewResult, error)
	// HistoryByQuestion returns every result for one question, newest first.
	HistoryByQuestion(ctx context.Context, questionID int64) ([]*ReviewResult, error)
	DeleteByAssessment(ctx context.Context, assessmentID int64) error
}

// SummaryRepository persists assessment-level aggregates.
type SummaryRepository interface {
	Upsert(ctx context.Context, s *AssessmentSummary) error
	GetByAssessment(ctx context.Context, assessmentID int64) (*AssessmentSummary, error)
}

// RubricRepository persists rubrics and their attachment to assessments.
type RubricRepository interface {
	Create(ctx context.Context, r *Rubric) error
	Update(ctx context.Context, r *Rubric) error
	GetByID(ctx context.Context, id int64) (*Rubric, error)
	ListReusable(ctx context.Context) ([]*Rubric, error)
	Delete(ctx context.Context, id int64) error

	AttachToAssessment(ctx context.Context, assessmentID, rubricID int64) error
	DetachFromAssessment(ctx context.Context, assessmentID int64) error
	GetForAssessment(ctx context.Context, assessmentID int64) (*Rubric, error)
}

// JobRepository persists background review runs.
type JobRepository interface {
	Create(ctx context.Context, j *ReviewJob) error
	GetByID(ctx context.Context, id int64) (*ReviewJob, error)
	// LatestByAssessment returns the most recent job for an assessment.
	LatestByAssessment(ctx context.Context, assessmentID int64) (*ReviewJob, error)
	// ClaimNext atomically moves one queued job to running and returns it.
	// Returns ErrNotFound when the queue is empty.
	ClaimNext(ctx context.Context) (*ReviewJob, error)
	UpdateProgress(ctx context.Context, id int64, done, failed int, stage string) error
	Heartbeat(ctx context.Context, id int64) error
	Finish(ctx context.Context, id int64, status JobStatus, errMsg string) error
	// ReclaimStalled returns running jobs whose worker stopped heartbeating to
	// the queue, so a restarted process picks them up instead of leaving an
	// assessment stuck in `reviewing` forever.
	ReclaimStalled(ctx context.Context, olderThan time.Duration) (int, error)
	// HasActive reports whether a queued or running job exists for the
	// assessment, so the UI can refuse a duplicate trigger.
	HasActive(ctx context.Context, assessmentID int64) (bool, error)
}

// UserRepository persists the internal team's login accounts.
type UserRepository interface {
	Create(ctx context.Context, u *User) error
	Update(ctx context.Context, u *User) error
	GetByID(ctx context.Context, id int64) (*User, error)
	GetByUsername(ctx context.Context, username string) (*User, error)
	Count(ctx context.Context) (int, error)
	RecordLogin(ctx context.Context, id int64, at time.Time) error

	// RecoveryCodes returns the stored bcrypt hashes of a user's unused MFA
	// recovery codes. The plaintext codes are shown once at enrolment and
	// never persisted.
	RecoveryCodes(ctx context.Context, userID int64) ([]string, error)
	// ReplaceRecoveryCodes swaps the whole set, invalidating any previous
	// codes. Passing nil clears them.
	ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error
	// ConsumeRecoveryCode removes one used code so it cannot be replayed.
	ConsumeRecoveryCode(ctx context.Context, userID int64, hash string) error
}

// SessionRepository persists login sessions server-side.
type SessionRepository interface {
	Create(ctx context.Context, s *Session) error
	GetByID(ctx context.Context, id string) (*Session, error)
	// Promote clears the MFA-pending flag once a TOTP code has been verified.
	Promote(ctx context.Context, id string, expiresAt time.Time) error
	Delete(ctx context.Context, id string) error
	DeleteExpired(ctx context.Context, before time.Time) (int, error)
}

// Repositories bundles every repository for constructor injection.
type Repositories struct {
	Tx          TxManager
	Vendors     VendorRepository
	Domains     DomainRepository
	Assessments AssessmentRepository
	Questions   QuestionRepository
	Results     ReviewResultRepository
	Summaries   SummaryRepository
	Rubrics     RubricRepository
	Jobs        JobRepository
	Users       UserRepository
	Sessions    SessionRepository
}
