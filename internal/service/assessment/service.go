// Package assessment holds the business logic for vendors, assessments and
// questionnaire ingestion. It depends only on the interfaces in
// internal/domain, so it can be tested without a database or an HTTP server.
package assessment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/repository"
	"third-party-review/internal/service"
	"third-party-review/internal/service/parser"

	"github.com/google/uuid"
)

// Service implements vendor and assessment use cases.
// Step 3 - Implement the Struct and its Methods.
//
// Every field is an interface from Deps and is unexported: once constructed,
// nothing outside this package can swap a dependency out from under a running
// service.
type Service struct {
	tx                helper.TxManager
	vendors           repository.VendorRepository
	domains           repository.AssessmentDomainRepository
	assessments       repository.AssessmentRepository
	questions         repository.QuestionRepository
	results           repository.ReviewResultRepository
	summaries         repository.AssessmentSummaryRepository
	rubrics           repository.RubricRepository
	assessmentRubrics repository.AssessmentRubricRepository
	uploads           repository.UploadRepository
	parser            service.QuestionnaireParser
	log               *slog.Logger
}

// Step 4 - Constructor ensuring the dependency is injected.
//
// New returns an error rather than panicking or accepting a half-built Deps,
// so an incomplete wiring fails at startup naming the missing dependency.
func New(deps Deps) (*Service, error) {
	if err := deps.validate(); err != nil {
		return nil, err
	}
	return &Service{
		tx:                deps.Tx,
		vendors:           deps.Vendors,
		domains:           deps.Domains,
		assessments:       deps.Assessments,
		questions:         deps.Questions,
		results:           deps.Results,
		summaries:         deps.Summaries,
		rubrics:           deps.Rubrics,
		assessmentRubrics: deps.AssessmentRubrics,
		uploads:           deps.Uploads,
		parser:            deps.Parser,
		log:               deps.Log,
	}, nil
}

// MustNew is New for wiring that cannot meaningfully recover, such as a test
// fixture. It panics on a missing dependency.
func MustNew(deps Deps) *Service {
	s, err := New(deps)
	if err != nil {
		panic(err)
	}
	return s
}

// ---------------------------------------------------------------------------
// Vendors
// ---------------------------------------------------------------------------

// CreateVendor registers a third party.
func (s *Service) CreateVendor(ctx context.Context, v *model.Vendor) error {
	if err := s.vendors.Create(ctx, v); err != nil {
		if errors.Is(err, helper.ErrAlreadyExists) {
			return helper.ValidationError{Field: "name", Message: "A vendor with that name already exists."}
		}
		return err
	}
	return nil
}

// UpdateVendor edits a third party.
func (s *Service) UpdateVendor(ctx context.Context, v *model.Vendor) error {
	if err := s.vendors.Update(ctx, v); err != nil {
		if errors.Is(err, helper.ErrAlreadyExists) {
			return helper.ValidationError{Field: "name", Message: "A vendor with that name already exists."}
		}
		return err
	}
	return nil
}

// ListVendors returns vendors matching an optional search string.
func (s *Service) ListVendors(ctx context.Context, search string, limit, offset int) ([]*model.Vendor, int, error) {
	vendors, err := s.vendors.List(ctx, search, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.vendors.Count(ctx, search)
	if err != nil {
		return nil, 0, err
	}
	return vendors, total, nil
}

// GetVendor returns one vendor.
func (s *Service) GetVendor(ctx context.Context, id uuid.UUID) (*model.Vendor, error) {
	return s.vendors.GetByID(ctx, id)
}

// DeleteVendor removes a vendor and, by cascade, its assessments.
func (s *Service) DeleteVendor(ctx context.Context, id uuid.UUID) error {
	return s.vendors.Delete(ctx, id)
}

// ---------------------------------------------------------------------------
// Assessments
// ---------------------------------------------------------------------------

// ListAssessments returns assessments matching a filter, with the total count.
func (s *Service) ListAssessments(ctx context.Context, f dto.AssessmentFilter) ([]*model.Assessment, int, error) {
	items, err := s.assessments.List(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	total, err := s.assessments.Count(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

// GetAssessment returns one assessment with its summary attached when present.
func (s *Service) GetAssessment(ctx context.Context, id uuid.UUID) (*model.Assessment, error) {
	a, err := s.assessments.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if sum, err := s.summaries.GetByAssessment(ctx, id); err == nil {
		a.Summary = sum
	} else if !errors.Is(err, helper.ErrNotFound) {
		return nil, err
	}
	return a, nil
}

// DeleteAssessment removes an assessment and everything hanging off it.
func (s *Service) DeleteAssessment(ctx context.Context, id uuid.UUID) error {
	return s.assessments.Delete(ctx, id)
}

// ListDomains returns the seeded TPSA domains.
func (s *Service) ListDomains(ctx context.Context) ([]*model.AssessmentDomain, error) {
	return s.domains.List(ctx, false)
}

// ---------------------------------------------------------------------------
// Ingestion
// ---------------------------------------------------------------------------

// maxUploadBytes is a hard ceiling applied regardless of the configured
// multipart limit, so a pathological file cannot be read fully into memory.
const maxUploadBytes = 64 << 20

// Upload stores a questionnaire against a new assessment. It deliberately does
// not parse yet: parsing happens in Preview, so a parse failure leaves a
// recoverable assessment the user can retry or point at another worksheet,
// rather than losing the upload entirely.
func (s *Service) Upload(ctx context.Context, vendorID uuid.UUID, title, filename, contentType string, r io.Reader) (*model.Assessment, error) {
	if _, err := s.vendors.GetByID(ctx, vendorID); err != nil {
		if errors.Is(err, helper.ErrNotFound) {
			return nil, helper.ValidationError{Field: "vendor_id", Message: "That vendor no longer exists."}
		}
		return nil, err
	}

	content, err := io.ReadAll(io.LimitReader(r, maxUploadBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if len(content) == 0 {
		return nil, helper.ValidationError{Field: "file", Message: "The uploaded file is empty."}
	}
	if len(content) > maxUploadBytes {
		return nil, helper.ValidationError{Field: "file", Message: "That file is too large to process."}
	}

	sum := sha256.Sum256(content)
	checksum := hex.EncodeToString(sum[:])

	if strings.TrimSpace(title) == "" {
		title = defaultTitle(filename)
	}

	a := &model.Assessment{
		VendorID:       vendorID,
		Title:          title,
		Status:         model.StatusUploaded,
		SourceFilename: filename,
		SourceSize:     int64(len(content)),
		SourceSHA256:   checksum,
	}

	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.assessments.Create(ctx, a); err != nil {
			return err
		}
		return s.uploads.Save(ctx, &model.Upload{
			AssessmentID: a.ID,
			Filename:     filename,
			ContentType:  contentType,
			Content:      content,
			ByteSize:     int64(len(content)),
			SHA256:       checksum,
		})
	})
	if err != nil {
		return nil, err
	}
	s.log.Info("questionnaire uploaded",
		"assessment_id", a.ID, "vendor_id", vendorID, "filename", filename, "bytes", len(content))
	return a, nil
}

// Preview parses the stored upload and returns the confirmable preview. sheet
// selects a worksheet; empty means "whichever the parser picks".
func (s *Service) Preview(ctx context.Context, assessmentID uuid.UUID, sheet string) (*model.Assessment, *dto.IngestPreview, error) {
	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return nil, nil, err
	}
	upload, err := s.uploads.Get(ctx, assessmentID)
	if err != nil {
		if errors.Is(err, helper.ErrNotFound) {
			return a, nil, helper.ValidationError{
				Field:   "file",
				Message: "The original file is no longer stored for this assessment, so the mapping can't be changed.",
			}
		}
		return nil, nil, err
	}

	domains, err := s.domains.List(ctx, false)
	if err != nil {
		return nil, nil, err
	}
	if len(domains) == 0 {
		return nil, nil, fmt.Errorf("no assessment domains are seeded; run migrations")
	}

	if sheet == "" {
		sheet = upload.SheetName
	}

	var preview *dto.IngestPreview
	if sheet != "" && isExcel(upload.Filename) {
		grid, gErr := parser.ReadExcelSheet(upload.Content, sheet)
		if gErr != nil {
			return a, nil, gErr
		}
		preview, err = s.parser.PreviewGrid(grid, domains)
	} else {
		preview, err = s.parser.Parse(bytes.NewReader(upload.Content), upload.Filename, domains)
	}
	if err != nil {
		return a, nil, err
	}

	// Re-apply a mapping the user already confirmed, so returning to the step
	// shows their corrections rather than the original guesses.
	if a.ColumnMapping != nil && len(a.ColumnMapping.Bindings) > 0 &&
		a.ColumnMapping.SheetName == preview.Grid.SheetName {
		preview.Mapping = a.ColumnMapping
		preview.HeaderRow = a.ColumnMapping.HeaderRow
		preview.SyncCandidates(a.ColumnMapping)
		preview.Rows, preview.Sections, preview.Warnings =
			parser.DetectSections(preview.Grid, preview.Mapping, domains)
	}
	return a, preview, nil
}

// ConfirmMapping persists the user-confirmed mapping and the resulting
// questions, moving the assessment to `mapped`. Re-confirming replaces the
// previously ingested questions, which also discards any AI results attached
// to them - the caller is expected to have warned the user.
func (s *Service) ConfirmMapping(
	ctx context.Context,
	assessmentID uuid.UUID,
	mapping *model.ColumnMapping,
	domainOverride map[int]uuid.UUID,
	sheet string,
) (int, error) {
	if err := service.ValidateColumnMapping(mapping); err != nil {
		return 0, err
	}

	a, err := s.assessments.GetByID(ctx, assessmentID)
	if err != nil {
		return 0, err
	}
	if a.Status == model.StatusReviewing {
		return 0, helper.ValidationError{
			Field:   "status",
			Message: "An AI review is running for this assessment. Wait for it to finish before re-mapping.",
		}
	}

	upload, err := s.uploads.Get(ctx, assessmentID)
	if err != nil {
		return 0, err
	}
	domains, err := s.domains.List(ctx, false)
	if err != nil {
		return 0, err
	}

	var grid *dto.Grid
	if sheet != "" && isExcel(upload.Filename) {
		grid, err = parser.ReadExcelSheet(upload.Content, sheet)
	} else {
		grid, err = parser.ReadGrid(bytes.NewReader(upload.Content), upload.Filename)
	}
	if err != nil {
		return 0, err
	}

	questions, sections, err := s.parser.Apply(grid, mapping, domains, domainOverride, assessmentID)
	if err != nil {
		return 0, err
	}

	mapping.SheetName = grid.SheetName
	mapping.ConfirmedAt = time.Now().UTC().Format(time.RFC3339)

	err = s.tx.RunInTx(ctx, func(ctx context.Context) error {
		// Replacing the question set invalidates any results keyed to the old
		// rows, so they are removed together rather than left orphaned.
		if err := s.results.DeleteByAssessment(ctx, assessmentID); err != nil {
			return err
		}
		if err := s.questions.DeleteByAssessment(ctx, assessmentID); err != nil {
			return err
		}
		if err := s.questions.BulkCreate(ctx, questions); err != nil {
			return err
		}
		if err := s.assessments.SaveColumnMapping(ctx, assessmentID, mapping); err != nil {
			return err
		}
		if err := s.uploads.SetSheet(ctx, assessmentID, grid.SheetName); err != nil {
			return err
		}
		return s.assessments.SetStatus(ctx, assessmentID, model.StatusMapped, time.Now().UTC())
	})
	if err != nil {
		return 0, err
	}

	s.log.Info("questionnaire ingested",
		"assessment_id", assessmentID, "questions", len(questions), "sections", len(sections))
	return len(questions), nil
}

// ListQuestions returns the questions of an assessment with their latest AI
// result attached.
func (s *Service) ListQuestions(ctx context.Context, f dto.QuestionFilter) ([]*model.Question, error) {
	questions, err := s.questions.List(ctx, f)
	if err != nil {
		return nil, err
	}
	results, err := s.results.LatestByAssessment(ctx, f.AssessmentID, nil)
	if err != nil {
		return nil, err
	}
	for _, q := range questions {
		if r, ok := results[q.ID]; ok {
			q.LatestResult = r
		}
	}
	return questions, nil
}

// ReassignDomain moves one question to a different domain after ingestion.
func (s *Service) ReassignDomain(ctx context.Context, questionID, domainID uuid.UUID) error {
	return s.questions.SetDomain(ctx, questionID, domainID)
}

// defaultTitle derives an assessment title from the uploaded filename.
func defaultTitle(filename string) string {
	name := filename
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	name = strings.NewReplacer("_", " ", "-", " ").Replace(name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "Assessment " + time.Now().Format("2006-01-02")
	}
	return name
}

func isExcel(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".xlsx") ||
		strings.HasSuffix(lower, ".xlsm") ||
		strings.HasSuffix(lower, ".xltx")
}

// ---------------------------------------------------------------------------
// Rubrics
// ---------------------------------------------------------------------------

// AttachRubric stores a rubric and binds it to an assessment. A rubric with
// no ID is created first; one with an ID is attached as-is, which is how a
// reusable rubric is shared across assessments.
func (s *Service) AttachRubric(ctx context.Context, assessmentID uuid.UUID, rubric *model.Rubric) error {
	if _, err := s.assessments.GetByID(ctx, assessmentID); err != nil {
		return err
	}
	return s.tx.RunInTx(ctx, func(ctx context.Context) error {
		if rubric.ID == uuid.Nil {
			if err := s.rubrics.Create(ctx, rubric); err != nil {
				return err
			}
		}
		return s.assessmentRubrics.Attach(ctx, assessmentID, rubric.ID)
	})
}

// DetachRubric unbinds the rubric from an assessment.
func (s *Service) DetachRubric(ctx context.Context, assessmentID uuid.UUID) error {
	return s.assessmentRubrics.Detach(ctx, assessmentID)
}

// GetRubric returns the rubric attached to an assessment.
func (s *Service) GetRubric(ctx context.Context, assessmentID uuid.UUID) (*model.Rubric, error) {
	return s.assessmentRubrics.GetRubric(ctx, assessmentID)
}

// ListReusableRubrics returns rubrics available to any assessment.
func (s *Service) ListReusableRubrics(ctx context.Context) ([]*model.Rubric, error) {
	return s.rubrics.ListReusable(ctx)
}
