package assessment

import (
	"errors"
	"fmt"
	"log/slog"

	"third-party-review/internal/helper"
	"third-party-review/internal/repository"
	"third-party-review/internal/service"
)

type Deps struct {
	Tx          helper.TxManager
	Vendors     repository.VendorRepository
	Domains     repository.AssessmentDomainRepository
	Assessments repository.AssessmentRepository
	Uploads     repository.UploadRepository
	Questions   repository.QuestionRepository
	Results     repository.ReviewResultRepository
	Summaries   repository.AssessmentSummaryRepository
	Rubrics     repository.RubricRepository
	// AssessmentRubrics is the join: which rubric this assessment is judged
	// against. Separate from Rubrics because attaching one and editing one are
	// different tables and different concerns.
	AssessmentRubrics repository.AssessmentRubricRepository

	// Parser is the contract, not the spreadsheet implementation, so a test
	// can drive ingestion without building a real workbook.
	Parser service.QuestionnaireParser

	Log *slog.Logger
}

// ErrMissingDependency is returned by the constructor when the wiring is
// incomplete.
var ErrMissingDependency = errors.New("assessment: missing dependency")

// validate reports the first dependency that was not injected.
//
// Checking at construction turns a wiring mistake into a startup failure that
// names the missing field, instead of a nil-pointer panic on whichever request
// happens to reach that repository first - possibly weeks later, in production.
func (d Deps) validate() error {
	missing := ""
	switch {
	case d.Tx == nil:
		missing = "Tx"
	case d.Vendors == nil:
		missing = "Vendors"
	case d.Domains == nil:
		missing = "Domains"
	case d.Assessments == nil:
		missing = "Assessments"
	case d.Uploads == nil:
		missing = "Uploads"
	case d.Questions == nil:
		missing = "Questions"
	case d.Results == nil:
		missing = "Results"
	case d.Summaries == nil:
		missing = "Summaries"
	case d.Rubrics == nil:
		missing = "Rubrics"
	case d.AssessmentRubrics == nil:
		missing = "AssessmentRubrics"
	case d.Parser == nil:
		missing = "Parser"
	case d.Log == nil:
		missing = "Log"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMissingDependency, missing)
}

// FromRepositories builds Deps from the full repository set, which is what the
// main package has to hand. The service still only ever sees the contracts it
// declared above.
func FromRepositories(repos *repository.Repositories, p service.QuestionnaireParser, log *slog.Logger) Deps {
	if repos == nil {
		return Deps{Parser: p, Log: log}
	}
	return Deps{
		Tx:                repos.Tx,
		Vendors:           repos.Vendors,
		Domains:           repos.Domains,
		Assessments:       repos.Assessments,
		Uploads:           repos.Uploads,
		Questions:         repos.Questions,
		Results:           repos.Results,
		Summaries:         repos.Summaries,
		Rubrics:           repos.Rubrics,
		AssessmentRubrics: repos.AssessmentRubrics,
		Parser:            p,
		Log:               log,
	}
}
