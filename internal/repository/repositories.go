package repository

import "third-party-review/internal/helper"

// Repositories bundles every repository for constructor injection. A service
// takes only the contracts it needs (see each service's Deps struct); this
// bundle exists so main has one thing to build and pass along.
type Repositories struct {
	Tx                helper.TxManager
	Vendors           VendorRepository
	Domains           AssessmentDomainRepository
	Assessments       AssessmentRepository
	Uploads           UploadRepository
	Questions         QuestionRepository
	Results           ReviewResultRepository
	Summaries         AssessmentSummaryRepository
	Rubrics           RubricRepository
	AssessmentRubrics AssessmentRubricRepository
	Jobs              ReviewJobRepository
	Users             UserRepository
	RecoveryCodes     RecoveryCodeRepository
	Sessions          SessionRepository
	Settings          SettingsRepository
}

// NewRepositories builds the full repository set backed by db, each through
// its own constructor with the ConnProvider injected. db also satisfies
// helper.TxManager, so it is what every service's Deps.Tx is set to.
func NewRepositories(db *helper.DB) *Repositories {
	return &Repositories{
		Tx:                db,
		Vendors:           NewVendorRepository(db),
		Domains:           NewAssessmentDomainRepository(db),
		Assessments:       NewAssessmentRepository(db),
		Uploads:           NewUploadRepository(db),
		Questions:         NewQuestionRepository(db),
		Results:           NewReviewResultRepository(db),
		Summaries:         NewAssessmentSummaryRepository(db),
		Rubrics:           NewRubricRepository(db),
		AssessmentRubrics: NewAssessmentRubricRepository(db),
		Jobs:              NewReviewJobRepository(db),
		Users:             NewUserRepository(db),
		RecoveryCodes:     NewRecoveryCodeRepository(db),
		Sessions:          NewSessionRepository(db),
		Settings:          NewSettingsRepository(db),
	}
}
