package assessment

import (
	"errors"
	"log/slog"
	"os"
	"testing"

	"third-party-review/internal/helper"
	"third-party-review/internal/repository"
	"third-party-review/internal/service/parser"
)

// The point of validating in the constructor is that a wiring mistake fails at
// startup naming the missing dependency, instead of panicking on whichever
// request happens to reach that repository first. These tests pin that.
func TestNewRejectsIncompleteWiring(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	full := Deps{
		Tx:          stubTx{},
		Vendors:     stubVendors{},
		Domains:     stubDomains{},
		Assessments: stubAssessments{},
		Uploads:     stubUploads{},
		Questions:   stubQuestions{},
		Results:     stubResults{},
		Summaries:   stubSummaries{},
		Rubrics:     stubRubrics{},

		AssessmentRubrics: stubAssessmentRubrics{},

		Parser: parser.New(),
		Log:    log,
	}
	if _, err := New(full); err != nil {
		t.Fatalf("a complete Deps should construct, got %v", err)
	}

	// Each field, removed one at a time, must be reported by name.
	cases := map[string]func(Deps) Deps{
		"Tx":                func(d Deps) Deps { d.Tx = nil; return d },
		"Vendors":           func(d Deps) Deps { d.Vendors = nil; return d },
		"Domains":           func(d Deps) Deps { d.Domains = nil; return d },
		"Assessments":       func(d Deps) Deps { d.Assessments = nil; return d },
		"Uploads":           func(d Deps) Deps { d.Uploads = nil; return d },
		"Questions":         func(d Deps) Deps { d.Questions = nil; return d },
		"Results":           func(d Deps) Deps { d.Results = nil; return d },
		"Summaries":         func(d Deps) Deps { d.Summaries = nil; return d },
		"Rubrics":           func(d Deps) Deps { d.Rubrics = nil; return d },
		"AssessmentRubrics": func(d Deps) Deps { d.AssessmentRubrics = nil; return d },
		"Parser":            func(d Deps) Deps { d.Parser = nil; return d },
		"Log":               func(d Deps) Deps { d.Log = nil; return d },
	}
	for name, remove := range cases {
		_, err := New(remove(full))
		if err == nil {
			t.Errorf("New succeeded with %s missing", name)
			continue
		}
		if !errors.Is(err, ErrMissingDependency) {
			t.Errorf("%s: error = %v, want ErrMissingDependency", name, err)
		}
		// The message has to name the field, or it does not save anyone time.
		if !contains(err.Error(), name) {
			t.Errorf("%s: error %q does not name the missing dependency", name, err)
		}
	}
}

// FromRepositories is the bridge the main package uses; a nil set must not
// panic, it must produce a Deps the constructor then rejects.
func TestFromRepositoriesWithNilSet(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if _, err := New(FromRepositories(nil, parser.New(), log)); !errors.Is(err, ErrMissingDependency) {
		t.Errorf("error = %v, want ErrMissingDependency", err)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

// These stubs exist only to be non-nil. Each embeds a single contract - one
// type embedding several would make the method names they share ambiguous -
// and no method on any of them is ever called by these tests.
type (
	stubTx      struct{ helper.TxManager }
	stubVendors struct{ repository.VendorRepository }
	stubDomains struct {
		repository.AssessmentDomainRepository
	}
	stubAssessments struct {
		repository.AssessmentRepository
	}
	stubQuestions struct{ repository.QuestionRepository }
	stubResults   struct {
		repository.ReviewResultRepository
	}
	stubSummaries struct {
		repository.AssessmentSummaryRepository
	}
	stubRubrics struct{ repository.RubricRepository }

	stubUploads struct{ repository.UploadRepository }

	stubAssessmentRubrics struct {
		repository.AssessmentRubricRepository
	}
)
