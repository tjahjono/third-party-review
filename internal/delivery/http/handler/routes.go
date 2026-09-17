package handler

import (
	"net/http"

	"third-party-review/internal/service"
	"third-party-review/internal/service/assessment"
	"third-party-review/internal/service/auth"
	"third-party-review/internal/service/dashboard"
	"third-party-review/internal/service/review"
	"third-party-review/internal/service/settings"
)

// Routes is the delivery-layer contract: the set of HTTP handlers the router
// wires up. The router depends on this rather than on *Handler, so the routing
// table and the handler implementations can be changed or substituted
// independently - and a routing test can assert every path is reachable
// against a stub that records calls.
type Routes interface {
	// Dashboard
	DashboardPage(w http.ResponseWriter, r *http.Request)

	// Settings
	SettingsPage(w http.ResponseWriter, r *http.Request)
	UpdateRiskMatrix(w http.ResponseWriter, r *http.Request)

	// User management
	UsersPage(w http.ResponseWriter, r *http.Request)
	CreateAccount(w http.ResponseWriter, r *http.Request)
	RenameAccount(w http.ResponseWriter, r *http.Request)
	ResetAccountPassword(w http.ResponseWriter, r *http.Request)
	ResetAccountMFA(w http.ResponseWriter, r *http.Request)
	DeactivateAccount(w http.ResponseWriter, r *http.Request)
	ReactivateAccount(w http.ResponseWriter, r *http.Request)

	// Authentication and account
	LoginPage(w http.ResponseWriter, r *http.Request)
	Login(w http.ResponseWriter, r *http.Request)
	MFAPage(w http.ResponseWriter, r *http.Request)
	VerifyMFA(w http.ResponseWriter, r *http.Request)
	Logout(w http.ResponseWriter, r *http.Request)
	FirstRunSetup(w http.ResponseWriter, r *http.Request)
	AccountPage(w http.ResponseWriter, r *http.Request)
	ChangePassword(w http.ResponseWriter, r *http.Request)
	UpdateLanguage(w http.ResponseWriter, r *http.Request)
	BeginMFA(w http.ResponseWriter, r *http.Request)
	ConfirmMFA(w http.ResponseWriter, r *http.Request)
	DisableMFA(w http.ResponseWriter, r *http.Request)
	RegenerateRecoveryCodes(w http.ResponseWriter, r *http.Request)

	// Vendors
	ListVendors(w http.ResponseWriter, r *http.Request)
	CreateVendor(w http.ResponseWriter, r *http.Request)
	DeleteVendor(w http.ResponseWriter, r *http.Request)

	// Assessments and ingestion
	ListAssessments(w http.ResponseWriter, r *http.Request)
	NewAssessment(w http.ResponseWriter, r *http.Request)
	UploadAssessment(w http.ResponseWriter, r *http.Request)
	AssessmentDetail(w http.ResponseWriter, r *http.Request)
	DeleteAssessment(w http.ResponseWriter, r *http.Request)
	MappingPage(w http.ResponseWriter, r *http.Request)
	RepreviewMapping(w http.ResponseWriter, r *http.Request)
	ConfirmMapping(w http.ResponseWriter, r *http.Request)

	// AI review
	StartReview(w http.ResponseWriter, r *http.Request)
	ReviewStatus(w http.ResponseWriter, r *http.Request)
	QuestionList(w http.ResponseWriter, r *http.Request)
	SummaryPanel(w http.ResponseWriter, r *http.Request)

	// Human sign-off
	SignOffProgress(w http.ResponseWriter, r *http.Request)
	EditQuestion(w http.ResponseWriter, r *http.Request)
	CancelEditQuestion(w http.ResponseWriter, r *http.Request)
	FinalizeQuestion(w http.ResponseWriter, r *http.Request)
	ReopenQuestion(w http.ResponseWriter, r *http.Request)
	BulkFinalize(w http.ResponseWriter, r *http.Request)
	CloseAssessment(w http.ResponseWriter, r *http.Request)
	ReopenAssessment(w http.ResponseWriter, r *http.Request)
	ExportCSV(w http.ResponseWriter, r *http.Request)
	ExportXLSX(w http.ResponseWriter, r *http.Request)
	UploadAnswerRevisions(w http.ResponseWriter, r *http.Request)
	ApplyAnswerRevisions(w http.ResponseWriter, r *http.Request)

	// Rubric
	RubricPanel(w http.ResponseWriter, r *http.Request)
	AttachRubric(w http.ResponseWriter, r *http.Request)
	EditRubric(w http.ResponseWriter, r *http.Request)
	CancelEditRubric(w http.ResponseWriter, r *http.Request)
	UpdateRubric(w http.ResponseWriter, r *http.Request)
	DetachRubric(w http.ResponseWriter, r *http.Request)
}

// Compile-time proof that every contract is satisfied. A signature drift then
// fails the build here, next to the declaration, rather than at the wiring
// site in main or - worse - only when a stub is written months later.
//
// The service assertions live in this package rather than in domain because
// domain must not import the service packages; this is the layer that knows
// both sides.
var (
	_ Routes                   = (*Handler)(nil)
	_ service.AssessmentFacade = (*assessment.Service)(nil)
	_ service.VendorService    = (*assessment.Service)(nil)
	_ service.IngestService    = (*assessment.Service)(nil)
	_ service.SignOffService   = (*assessment.Service)(nil)
	_ service.RubricService    = (*assessment.Service)(nil)
	_ service.ReviewService    = (*review.Service)(nil)
	_ service.DashboardService = (*dashboard.Service)(nil)
	_ service.AuthService      = (*auth.Service)(nil)
	_ service.SettingsService  = (*settings.Service)(nil)
)
