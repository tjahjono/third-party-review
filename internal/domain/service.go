package domain

import (
	"context"
	"io"
)

// Service contracts.
//
// These sit beside the repository contracts in this package so that every
// boundary in the application is described in one dependency-free place:
// delivery depends on these, the service packages implement them, and neither
// imports the other. Swapping or stubbing an implementation touches nothing
// above or below it.
//
// This is why the ingestion model, the sign-off result types and the auth
// result types live in domain rather than in the packages that produce them.
// A contract naming a service-owned type would force this package to import a
// service package, inverting the dependency direction the layout exists to
// keep.
//
// They are split by concern rather than exposed as one large interface: the
// vendor screens need nothing from the review pipeline, and a test for them
// should not have to satisfy it.

// VendorService manages the third parties under assessment.
type VendorService interface {
	CreateVendor(ctx context.Context, v *Vendor) error
	UpdateVendor(ctx context.Context, v *Vendor) error
	GetVendor(ctx context.Context, id int64) (*Vendor, error)
	ListVendors(ctx context.Context, search string, limit, offset int) ([]*Vendor, int, error)
	DeleteVendor(ctx context.Context, id int64) error
}

// IngestService covers upload through to a confirmed, persisted questionnaire.
// Preview never writes: the caller shows it, takes the user's corrections, and
// commits them with ConfirmMapping.
type IngestService interface {
	Upload(ctx context.Context, vendorID int64, title, filename, contentType string, r io.Reader) (*Assessment, error)
	Preview(ctx context.Context, assessmentID int64, sheet string) (*Assessment, *IngestPreview, error)
	ConfirmMapping(ctx context.Context, assessmentID int64, mapping *ColumnMapping, domainOverride map[int]int64, sheet string) (int, error)
	ListDomains(ctx context.Context) ([]*AssessmentDomain, error)
}

// AssessmentService covers the lifecycle of an assessment and its questions.
type AssessmentService interface {
	ListAssessments(ctx context.Context, f AssessmentFilter) ([]*Assessment, int, error)
	GetAssessment(ctx context.Context, id int64) (*Assessment, error)
	DeleteAssessment(ctx context.Context, id int64) error
	ListQuestions(ctx context.Context, f QuestionFilter) ([]*Question, error)
	GetQuestion(ctx context.Context, questionID int64) (*Question, error)
	ReassignDomain(ctx context.Context, questionID, domainID int64) error
}

// SignOffService is the human-in-the-loop half: turning AI drafts into
// feedback a person has signed, and closing the result as the record.
type SignOffService interface {
	FinalizeQuestion(ctx context.Context, questionID int64, feedback string, userID *int64) (*Question, error)
	ReopenQuestion(ctx context.Context, questionID int64) (*Question, error)
	BulkFinalize(ctx context.Context, assessmentID int64, domainID *int64, userID *int64) (BulkFinalizeResult, error)
	Progress(ctx context.Context, assessmentID int64) (SignOffProgress, error)
	CloseAssessment(ctx context.Context, assessmentID int64) error
	ReopenAssessment(ctx context.Context, assessmentID int64) error
	ExportCSV(ctx context.Context, assessmentID int64, w io.Writer) error
}

// RubricService manages the optional policy an assessment is judged against.
type RubricService interface {
	AttachRubric(ctx context.Context, assessmentID int64, rubric *Rubric) error
	DetachRubric(ctx context.Context, assessmentID int64) error
	GetRubric(ctx context.Context, assessmentID int64) (*Rubric, error)
	ListReusableRubrics(ctx context.Context) ([]*Rubric, error)
}

// AssessmentFacade is the union the delivery layer holds. The split contracts
// above are what a test should stub - one concern at a time.
type AssessmentFacade interface {
	VendorService
	IngestService
	AssessmentService
	SignOffService
	RubricService
}

// ReviewService orchestrates AI review runs. Run and Finish are the worker's
// half of the contract and are paired: every caller of Run must call Finish,
// because a panic inside Run has to be caught before the outcome is recorded.
type ReviewService interface {
	Enqueue(ctx context.Context, assessmentID int64) (*ReviewJob, error)
	Status(ctx context.Context, assessmentID int64) (*ReviewJob, error)
	Run(ctx context.Context, job *ReviewJob) error
	Finish(ctx context.Context, job *ReviewJob, runErr error) error
	RecomputeSummary(ctx context.Context, assessmentID int64) (*AssessmentSummary, error)
}

// AuthService covers login, sessions and the optional second factor.
type AuthService interface {
	Login(ctx context.Context, username, password, userAgent, ip string) (*LoginResult, error)
	VerifyMFA(ctx context.Context, sessionID, code string) (*User, error)
	Authenticate(ctx context.Context, sessionID string) (*User, *Session, error)
	PendingSession(ctx context.Context, sessionID string) (*Session, error)
	Logout(ctx context.Context, sessionID string) error
	PurgeExpiredSessions(ctx context.Context) (int, error)

	CreateUser(ctx context.Context, username, displayName, password string) (*User, error)
	ChangePassword(ctx context.Context, userID int64, current, next string) error
	GetUser(ctx context.Context, id int64) (*User, error)
	UserCount(ctx context.Context) (int, error)
	Bootstrap(ctx context.Context, username, password string) error

	BeginMFAEnrolment(ctx context.Context, userID int64) (*MFAEnrolment, error)
	CompleteMFAEnrolment(ctx context.Context, userID int64, secret, code string) ([]string, error)
	DisableMFA(ctx context.Context, userID int64, password string) error
	RegenerateRecoveryCodes(ctx context.Context, userID int64, password string) ([]string, error)
}

// QuestionnaireParser turns an uploaded file into a confirmable preview and,
// once a mapping is confirmed, into persistable questions. Declaring it here
// lets the ingestion service be tested against a stub parser, and lets the
// spreadsheet implementation be replaced without touching the service.
type QuestionnaireParser interface {
	Parse(r io.Reader, filename string, domains []*AssessmentDomain) (*IngestPreview, error)
	PreviewGrid(grid *Grid, domains []*AssessmentDomain) (*IngestPreview, error)
	Apply(grid *Grid, mapping *ColumnMapping, domains []*AssessmentDomain,
		domainOverride map[int]int64, assessmentID int64) ([]*Question, []Section, error)
}
