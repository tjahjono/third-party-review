package service

import (
	"context"
	"io"

	"github.com/google/uuid"

	"third-party-review/internal/dto"
	"third-party-review/internal/model"
)

// VendorService manages the third parties under assessment.
type VendorService interface {
	CreateVendor(ctx context.Context, v *model.Vendor) error
	UpdateVendor(ctx context.Context, v *model.Vendor) error
	GetVendor(ctx context.Context, id uuid.UUID) (*model.Vendor, error)
	ListVendors(ctx context.Context, search string, limit, offset int) ([]*model.Vendor, int, error)
	DeleteVendor(ctx context.Context, id uuid.UUID) error
}

// IngestService covers upload through to a confirmed, persisted questionnaire.
// Preview never writes: the caller shows it, takes the user's corrections, and
// commits them with ConfirmMapping.
type IngestService interface {
	Upload(ctx context.Context, vendorID uuid.UUID, title, filename, contentType string, r io.Reader) (*model.Assessment, error)
	Preview(ctx context.Context, assessmentID uuid.UUID, sheet string) (*model.Assessment, *dto.IngestPreview, error)
	ConfirmMapping(ctx context.Context, assessmentID uuid.UUID, mapping *model.ColumnMapping, domainOverride map[int]uuid.UUID, sheet string) (int, error)
	ListDomains(ctx context.Context) ([]*model.AssessmentDomain, error)
}

// AssessmentService covers the lifecycle of an assessment and its questions.
type AssessmentService interface {
	ListAssessments(ctx context.Context, f dto.AssessmentFilter) ([]*model.Assessment, int, error)
	GetAssessment(ctx context.Context, id uuid.UUID) (*model.Assessment, error)
	DeleteAssessment(ctx context.Context, id uuid.UUID) error
	ListQuestions(ctx context.Context, f dto.QuestionFilter) ([]*model.Question, error)
	GetQuestion(ctx context.Context, questionID uuid.UUID) (*model.Question, error)
	ReassignDomain(ctx context.Context, questionID, domainID uuid.UUID) error
}

// SignOffService is the human-in-the-loop half: turning AI drafts into
// feedback a person has signed, and closing the result as the record.
type SignOffService interface {
	FinalizeQuestion(ctx context.Context, questionID uuid.UUID, feedback string, userID *uuid.UUID) (*model.Question, error)
	ReopenQuestion(ctx context.Context, questionID uuid.UUID) (*model.Question, error)
	BulkFinalize(ctx context.Context, assessmentID uuid.UUID, domainID *uuid.UUID, userID *uuid.UUID) (dto.BulkFinalizeResult, error)
	Progress(ctx context.Context, assessmentID uuid.UUID) (dto.SignOffProgress, error)
	CloseAssessment(ctx context.Context, assessmentID uuid.UUID) error
	ReopenAssessment(ctx context.Context, assessmentID uuid.UUID) error
	ExportCSV(ctx context.Context, assessmentID uuid.UUID, w io.Writer) error
}

// RubricService manages the optional policy an assessment is judged against.
type RubricService interface {
	AttachRubric(ctx context.Context, assessmentID uuid.UUID, rubric *model.Rubric) error
	// UpdateRubric saves edits to an already-attached rubric in place. Because
	// a reusable rubric can be attached to more than one assessment, this
	// updates the shared rubrics row - every assessment using it sees the
	// change, rather than only the one it was edited from.
	UpdateRubric(ctx context.Context, rubric *model.Rubric) error
	DetachRubric(ctx context.Context, assessmentID uuid.UUID) error
	GetRubric(ctx context.Context, assessmentID uuid.UUID) (*model.Rubric, error)
	ListReusableRubrics(ctx context.Context) ([]*model.Rubric, error)
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
	Enqueue(ctx context.Context, assessmentID uuid.UUID, scope model.ReviewScope, questionIDs []uuid.UUID) (*model.ReviewJob, error)
	Status(ctx context.Context, assessmentID uuid.UUID) (*model.ReviewJob, error)
	Run(ctx context.Context, job *model.ReviewJob) error
	Finish(ctx context.Context, job *model.ReviewJob, runErr error) error
	RecomputeSummary(ctx context.Context, assessmentID uuid.UUID) (*model.AssessmentSummary, error)
}

// DashboardService aggregates cross-assessment figures for the home page.
type DashboardService interface {
	Build(ctx context.Context) (*dto.Dashboard, error)
}

// AuthService covers login, sessions and the optional second factor.
type AuthService interface {
	Login(ctx context.Context, username, password, userAgent, ip string) (*dto.LoginResult, error)
	VerifyMFA(ctx context.Context, sessionID, code string) (*model.User, error)
	Authenticate(ctx context.Context, sessionID string) (*model.User, *model.Session, error)
	PendingSession(ctx context.Context, sessionID string) (*model.Session, error)
	Logout(ctx context.Context, sessionID string) error
	PurgeExpiredSessions(ctx context.Context) (int, error)

	CreateUser(ctx context.Context, username, displayName, password string) (*model.User, error)
	ChangePassword(ctx context.Context, userID uuid.UUID, current, next string) error
	GetUser(ctx context.Context, id uuid.UUID) (*model.User, error)
	UserCount(ctx context.Context) (int, error)
	Bootstrap(ctx context.Context, username, password string) error

	BeginMFAEnrolment(ctx context.Context, userID uuid.UUID) (*dto.MFAEnrolment, error)
	CompleteMFAEnrolment(ctx context.Context, userID uuid.UUID, secret, code string) ([]string, error)
	DisableMFA(ctx context.Context, userID uuid.UUID, password string) error
	RegenerateRecoveryCodes(ctx context.Context, userID uuid.UUID, password string) ([]string, error)
}

// QuestionnaireParser turns an uploaded file into a confirmable preview and,
// once a mapping is confirmed, into persistable questions. Declaring it here
// lets the ingestion service be tested against a stub parser, and lets the
// spreadsheet implementation be replaced without touching the service.
type QuestionnaireParser interface {
	Parse(r io.Reader, filename string, domains []*model.AssessmentDomain) (*dto.IngestPreview, error)
	PreviewGrid(grid *dto.Grid, domains []*model.AssessmentDomain) (*dto.IngestPreview, error)
	Apply(grid *dto.Grid, mapping *model.ColumnMapping, domains []*model.AssessmentDomain, domainOverride map[int]uuid.UUID, assessmentID uuid.UUID) ([]*model.Question, []dto.Section, error)
}
