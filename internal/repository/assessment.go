package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type AssessmentRepository interface {
	Create(ctx context.Context, assessment *model.Assessment) error
	Update(ctx context.Context, assessment *model.Assessment) error
	GetByID(ctx context.Context, id uuid.UUID) (*model.Assessment, error)
	List(ctx context.Context, filter dto.AssessmentFilter) ([]*model.Assessment, error)
	Count(ctx context.Context, filter dto.AssessmentFilter) (int, error)
	SetStatus(ctx context.Context, id uuid.UUID, status model.AssessmentStatus, at time.Time) error
	SaveColumnMapping(ctx context.Context, id uuid.UUID, mapping *model.ColumnMapping) error
	SetCurrentRun(ctx context.Context, id uuid.UUID, runID uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type assessmentRepository struct {
	db helper.ConnProvider
}

func NewAssessmentRepository(db helper.ConnProvider) AssessmentRepository {
	return &assessmentRepository{db: db}
}

func (r *assessmentRepository) Create(ctx context.Context, assessment *model.Assessment) error {
	var mapping any

	if assessment.ColumnMapping != nil {
		encoded, err := json.Marshal(assessment.ColumnMapping)
		if err != nil {
			return fmt.Errorf("repository: encode column_mapping: %w", err)
		}

		mapping = encoded
	}

	err := r.db.Querier(ctx).QueryRow(ctx, `
		INSERT INTO assessments (
			vendor_id, title, status, source_filename,
			source_size, source_sha256, column_mapping
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at
	`,
		assessment.VendorID,
		assessment.Title,
		assessment.Status,
		assessment.SourceFilename,
		assessment.SourceSize,
		assessment.SourceSHA256,
		mapping,
	).Scan(
		&assessment.ID,
		&assessment.CreatedAt,
		&assessment.UpdatedAt,
	)

	return helper.MapErr(err)
}

func (r *assessmentRepository) Update(ctx context.Context, assessment *model.Assessment) error {
	err := r.db.Querier(ctx).QueryRow(ctx, `
		UPDATE assessments
		   SET vendor_id       = $2,
		       title           = $3,
		       status          = $4,
		       source_filename = $5,
		       source_size     = $6,
		       source_sha256   = $7
		 WHERE id = $1
		RETURNING updated_at
	`,
		assessment.ID,
		assessment.VendorID,
		assessment.Title,
		assessment.Status,
		assessment.SourceFilename,
		assessment.SourceSize,
		assessment.SourceSHA256,
	).Scan(
		&assessment.UpdatedAt,
	)

	return helper.MapErr(err)
}

func (r *assessmentRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.Assessment, error) {
	row := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT `+assessmentColumns+`, v.name
		  FROM assessments a
		  JOIN vendors v ON v.id = a.vendor_id
		 WHERE a.id = $1
	`, id)

	return scanAssessment(row, true, false)
}

func (r *assessmentRepository) List(ctx context.Context, filter dto.AssessmentFilter) ([]*model.Assessment, error) {
	where, args := assessmentWhere(filter)

	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	// The summary is joined rather than fetched per row: the index shows a
	// risk figure for every assessment, and doing that with one query per row
	// would be a request-per-assessment on the busiest page in the app.
	query := fmt.Sprintf(`
		SELECT %s, v.name,
		       s.overall_score, s.question_count, s.flagged_count,
		       s.incomplete_count, s.finalized_count
		  FROM assessments a
		  JOIN vendors v ON v.id = a.vendor_id
		  LEFT JOIN assessment_summaries s ON s.assessment_id = a.id
		 %s
		 ORDER BY a.created_at DESC
		 LIMIT $%d OFFSET $%d
	`, assessmentColumns, where, len(args)+1, len(args)+2)

	args = append(args, limit, filter.Offset)

	rows, err := r.db.Querier(ctx).Query(ctx, query, args...)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var assessments []*model.Assessment

	for rows.Next() {
		assessment, err := scanAssessment(rows, true, true)
		if err != nil {
			return nil, err
		}

		assessments = append(assessments, assessment)
	}

	return assessments, helper.MapErr(rows.Err())
}

func (r *assessmentRepository) Count(ctx context.Context, filter dto.AssessmentFilter) (int, error) {
	where, args := assessmentWhere(filter)

	var count int

	err := r.db.Querier(ctx).QueryRow(ctx, `
		SELECT COUNT(*)
		  FROM assessments a
		  JOIN vendors v ON v.id = a.vendor_id
	`+where, args...).Scan(
		&count,
	)

	return count, helper.MapErr(err)
}

func (r *assessmentRepository) SetStatus(ctx context.Context, id uuid.UUID, status model.AssessmentStatus, at time.Time) error {
	if !helper.ValidAssessmentStatus(status) {
		return fmt.Errorf("%w: unknown assessment status %q", helper.ErrInvalidInput, status)
	}

	// The timestamp columns are set positionally so the history of when an
	// assessment reached each stage survives later transitions.
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE assessments
		   SET status      = $2,
		       mapped_at   = CASE WHEN $2 = 'mapped'   THEN $3 ELSE mapped_at   END,
		       reviewed_at = CASE WHEN $2 = 'reviewed' THEN $3 ELSE reviewed_at END,
		       closed_at   = CASE WHEN $2 = 'closed'   THEN $3 ELSE closed_at   END
		 WHERE id = $1
	`, id, string(status), at)

	if err != nil {
		return helper.MapErr(err)
	}

	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}

	return nil
}

func (r *assessmentRepository) SaveColumnMapping(ctx context.Context, id uuid.UUID, mapping *model.ColumnMapping) error {
	encoded, err := json.Marshal(mapping)
	if err != nil {
		return fmt.Errorf("repository: encode column_mapping: %w", err)
	}

	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE assessments
		   SET column_mapping = $2
		 WHERE id = $1
	`, id, encoded)

	if err != nil {
		return helper.MapErr(err)
	}

	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}

	return nil
}

func (r *assessmentRepository) SetCurrentRun(ctx context.Context, id uuid.UUID, runID uuid.UUID) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		UPDATE assessments
		   SET current_run_id = $2
		 WHERE id = $1
	`, id, runID)

	if err != nil {
		return helper.MapErr(err)
	}

	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}

	return nil
}

func (r *assessmentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `
		DELETE FROM assessments
		 WHERE id = $1
	`, id)

	if err != nil {
		return helper.MapErr(err)
	}

	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}

	return nil
}

const assessmentColumns = `
	a.id, a.vendor_id, a.title, a.status, a.source_filename, a.source_size,
	a.source_sha256, a.column_mapping, a.current_run_id, a.created_at,
	a.updated_at, a.mapped_at, a.reviewed_at, a.closed_at`

// scanAssessment reads one row, optionally with the vendor name and the
// headline figures from the summary joined on.
func scanAssessment(row interface{ Scan(...any) error }, withVendor bool, withSummary bool) (*model.Assessment, error) {
	var (
		assessment model.Assessment
		mapping    []byte

		overallScore  *float64
		questionCnt   *int
		flaggedCnt    *int
		incompleteCnt *int
		finalizedCnt  *int
	)

	targets := []any{
		&assessment.ID, &assessment.VendorID, &assessment.Title, &assessment.Status,
		&assessment.SourceFilename, &assessment.SourceSize, &assessment.SourceSHA256,
		&mapping, &assessment.CurrentRunID, &assessment.CreatedAt, &assessment.UpdatedAt,
		&assessment.MappedAt, &assessment.ReviewedAt, &assessment.ClosedAt,
	}

	if withVendor {
		targets = append(targets, &assessment.VendorName)
	}

	if withSummary {
		targets = append(targets,
			&overallScore, &questionCnt, &flaggedCnt, &incompleteCnt, &finalizedCnt)
	}

	if err := row.Scan(targets...); err != nil {
		return nil, helper.MapErr(err)
	}

	// A left join yields NULLs for an assessment that has never been reviewed;
	// leaving Summary nil is what tells the UI to show a dash rather than a
	// misleading zero.
	if withSummary && overallScore != nil {
		assessment.Summary = &model.AssessmentSummary{
			AssessmentID:    assessment.ID,
			OverallScore:    *overallScore,
			QuestionCount:   derefInt(questionCnt),
			FlaggedCount:    derefInt(flaggedCnt),
			IncompleteCount: derefInt(incompleteCnt),
			FinalizedCount:  derefInt(finalizedCnt),
		}
	}

	if len(mapping) > 0 {
		var decoded model.ColumnMapping

		if err := json.Unmarshal(mapping, &decoded); err != nil {
			return nil, fmt.Errorf(
				"repository: decode column_mapping for assessment %s: %w", assessment.ID, err)
		}

		assessment.ColumnMapping = &decoded
	}

	return &assessment, nil
}

// assessmentWhere builds the filter clause shared by List and Count.
func assessmentWhere(filter dto.AssessmentFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)

	if filter.VendorID != nil {
		args = append(args, *filter.VendorID)
		clauses = append(clauses, fmt.Sprintf("a.vendor_id = $%d", len(args)))
	}

	if filter.Status != nil {
		args = append(args, string(*filter.Status))
		clauses = append(clauses, fmt.Sprintf("a.status = $%d", len(args)))
	}

	if search := strings.TrimSpace(filter.Search); search != "" {
		args = append(args, helper.LikeArg(search))
		clauses = append(clauses, fmt.Sprintf(
			`(a.title ILIKE $%d ESCAPE '\' OR v.name ILIKE $%d ESCAPE '\')`,
			len(args), len(args)))
	}

	if len(clauses) == 0 {
		return "", args
	}

	return "WHERE " + strings.Join(clauses, " AND "), args
}

// derefInt reads a nullable integer column, treating NULL as zero.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}

	return *p
}
