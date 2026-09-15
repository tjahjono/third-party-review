package review

import (
	"errors"
	"fmt"
	"log/slog"

	"third-party-review/internal/config"
	"third-party-review/internal/helper"
	"third-party-review/internal/repository"
	"third-party-review/internal/service"
)

// Step 1 - Define the Dependency Interface.
//
// The review pipeline needs the assessment and question repositories to read
// from, the result, summary and job repositories to write to, a rubric lookup,
// a transaction manager, and one AIReviewer. It deliberately does not take the
// vendor, user or session repositories: nothing in a review run touches them.
type Deps struct {
	Tx          helper.TxManager
	Assessments repository.AssessmentRepository
	Questions   repository.QuestionRepository
	Results     repository.ReviewResultRepository
	Summaries   repository.AssessmentSummaryRepository
	Rubrics     repository.AssessmentRubricRepository
	Jobs        repository.ReviewJobRepository

	// Reviewer is the provider-agnostic contract. Which concrete client sits
	// behind it - Open WebUI, OpenAI, Anthropic, the offline mock - is decided
	// at wiring time and is invisible here.
	Reviewer service.AIReviewer

	Config config.AI
	Log    *slog.Logger
}

// ErrMissingDependency is returned by the constructor when the wiring is
// incomplete.
var ErrMissingDependency = errors.New("review: missing dependency")

func (d Deps) validate() error {
	missing := ""
	switch {
	case d.Tx == nil:
		missing = "Tx"
	case d.Assessments == nil:
		missing = "Assessments"
	case d.Questions == nil:
		missing = "Questions"
	case d.Results == nil:
		missing = "Results"
	case d.Summaries == nil:
		missing = "Summaries"
	case d.Rubrics == nil:
		missing = "Rubrics"
	case d.Jobs == nil:
		missing = "Jobs"
	case d.Reviewer == nil:
		missing = "Reviewer"
	case d.Log == nil:
		missing = "Log"
	default:
		return nil
	}
	return fmt.Errorf("%w: %s", ErrMissingDependency, missing)
}

// FromRepositories builds Deps from the full repository set held by the main
// package.
func FromRepositories(repos *repository.Repositories, reviewer service.AIReviewer, cfg config.AI, log *slog.Logger) Deps {
	if repos == nil {
		return Deps{Reviewer: reviewer, Config: cfg, Log: log}
	}
	return Deps{
		Tx:          repos.Tx,
		Assessments: repos.Assessments,
		Questions:   repos.Questions,
		Results:     repos.Results,
		Summaries:   repos.Summaries,
		Rubrics:     repos.AssessmentRubrics,
		Jobs:        repos.Jobs,
		Reviewer:    reviewer,
		Config:      cfg,
		Log:         log,
	}
}
