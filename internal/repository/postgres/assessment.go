package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"third-party-review/internal/domain"
)

// AssessmentRepo is the PostgreSQL implementation of
// domain.AssessmentRepository.
type AssessmentRepo struct{ db *DB }

const assessmentCols = `
	a.id, a.vendor_id, a.title, a.status, a.source_filename, a.source_size,
	a.source_sha256, a.column_mapping, a.current_run_id, a.created_at,
	a.updated_at, a.mapped_at, a.reviewed_at, a.closed_at`

func scanAssessment(s interface{ Scan(...any) error }, withVendor bool) (*domain.Assessment, error) {
	return scanAssessmentRow(s, withVendor, false)
}

// scanAssessmentRow scans an assessment, optionally with the vendor name and
// the headline figures from its summary joined on.
func scanAssessmentRow(s interface{ Scan(...any) error }, withVendor, withSummary bool) (*domain.Assessment, error) {
	var (
		a       domain.Assessment
		mapping []byte

		overallScore  *float64
		questionCnt   *int
		flaggedCnt    *int
		incompleteCnt *int
		finalizedCnt  *int
	)
	targets := []any{
		&a.ID, &a.VendorID, &a.Title, &a.Status, &a.SourceFilename, &a.SourceSize,
		&a.SourceSHA256, &mapping, &a.CurrentRunID, &a.CreatedAt, &a.UpdatedAt,
		&a.MappedAt, &a.ReviewedAt, &a.ClosedAt,
	}
	if withVendor {
		targets = append(targets, &a.VendorName)
	}
	if withSummary {
		targets = append(targets, &overallScore, &questionCnt, &flaggedCnt, &incompleteCnt, &finalizedCnt)
	}
	if err := s.Scan(targets...); err != nil {
		return nil, mapErr(err)
	}
	// A left join yields NULLs for an assessment that has never been reviewed;
	// leaving Summary nil is what tells the UI to show a dash rather than a
	// misleading zero.
	if withSummary && overallScore != nil {
		a.Summary = &domain.AssessmentSummary{
			AssessmentID:    a.ID,
			OverallScore:    *overallScore,
			QuestionCount:   derefInt(questionCnt),
			FlaggedCount:    derefInt(flaggedCnt),
			IncompleteCount: derefInt(incompleteCnt),
			FinalizedCount:  derefInt(finalizedCnt),
		}
	}
	if len(mapping) > 0 {
		var m domain.ColumnMapping
		if err := json.Unmarshal(mapping, &m); err != nil {
			return nil, fmt.Errorf("postgres: decode column_mapping for assessment %d: %w", a.ID, err)
		}
		a.ColumnMapping = &m
	}
	return &a, nil
}

func (r *AssessmentRepo) Create(ctx context.Context, a *domain.Assessment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	var mapping any
	if a.ColumnMapping != nil {
		b, err := json.Marshal(a.ColumnMapping)
		if err != nil {
			return fmt.Errorf("postgres: encode column_mapping: %w", err)
		}
		mapping = b
	}
	const q = `
		INSERT INTO assessments (vendor_id, title, status, source_filename, source_size, source_sha256, column_mapping)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, a.VendorID, a.Title, a.Status, a.SourceFilename,
		a.SourceSize, a.SourceSHA256, mapping).Scan(&a.ID, &a.CreatedAt, &a.UpdatedAt)
	return mapErr(err)
}

func (r *AssessmentRepo) Update(ctx context.Context, a *domain.Assessment) error {
	if err := a.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE assessments
		   SET vendor_id = $2, title = $3, status = $4,
		       source_filename = $5, source_size = $6, source_sha256 = $7
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, a.ID, a.VendorID, a.Title, a.Status,
		a.SourceFilename, a.SourceSize, a.SourceSHA256).Scan(&a.UpdatedAt)
	return mapErr(err)
}

func (r *AssessmentRepo) GetByID(ctx context.Context, id int64) (*domain.Assessment, error) {
	const q = `
		SELECT ` + assessmentCols + `, v.name
		  FROM assessments a
		  JOIN vendors v ON v.id = a.vendor_id
		 WHERE a.id = $1`
	return scanAssessment(r.db.q(ctx).QueryRow(ctx, q, id), true)
}

func (r *AssessmentRepo) List(ctx context.Context, f domain.AssessmentFilter) ([]*domain.Assessment, error) {
	where, args := assessmentWhere(f)
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	// The summary is joined rather than fetched per row: the index shows a
	// risk figure for every assessment, and doing that with one query per row
	// would be a request-per-assessment on the busiest page in the app.
	q := fmt.Sprintf(`
		SELECT %s, v.name,
		       s.overall_score, s.question_count, s.flagged_count,
		       s.incomplete_count, s.finalized_count
		  FROM assessments a
		  JOIN vendors v ON v.id = a.vendor_id
		  LEFT JOIN assessment_summaries s ON s.assessment_id = a.id
		 %s
		 ORDER BY a.created_at DESC
		 LIMIT $%d OFFSET $%d`, assessmentCols, where, len(args)+1, len(args)+2)
	args = append(args, limit, f.Offset)

	rows, err := r.db.q(ctx).Query(ctx, q, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []*domain.Assessment
	for rows.Next() {
		a, err := scanAssessmentRow(rows, true, true)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, mapErr(rows.Err())
}

func (r *AssessmentRepo) Count(ctx context.Context, f domain.AssessmentFilter) (int, error) {
	where, args := assessmentWhere(f)
	q := `SELECT COUNT(*) FROM assessments a JOIN vendors v ON v.id = a.vendor_id ` + where
	var n int
	err := r.db.q(ctx).QueryRow(ctx, q, args...).Scan(&n)
	return n, mapErr(err)
}

// derefInt reads a nullable integer column, treating NULL as zero.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// assessmentWhere builds the shared filter clause for List and Count.
func assessmentWhere(f domain.AssessmentFilter) (string, []any) {
	var (
		clauses []string
		args    []any
	)
	if f.VendorID != nil {
		args = append(args, *f.VendorID)
		clauses = append(clauses, fmt.Sprintf("a.vendor_id = $%d", len(args)))
	}
	if f.Status != nil {
		args = append(args, string(*f.Status))
		clauses = append(clauses, fmt.Sprintf("a.status = $%d", len(args)))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		args = append(args, likeArg(s))
		clauses = append(clauses, fmt.Sprintf(`(a.title ILIKE $%d ESCAPE '\' OR v.name ILIKE $%d ESCAPE '\')`, len(args), len(args)))
	}
	if len(clauses) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(clauses, " AND "), args
}

func (r *AssessmentRepo) SetStatus(ctx context.Context, id int64, status domain.AssessmentStatus, at time.Time) error {
	if !status.Valid() {
		return fmt.Errorf("%w: unknown assessment status %q", domain.ErrInvalidInput, status)
	}
	// The timestamp columns are set positionally so the history of when an
	// assessment reached each stage survives later transitions.
	const q = `
		UPDATE assessments
		   SET status      = $2,
		       mapped_at   = CASE WHEN $2 = 'mapped'   THEN $3 ELSE mapped_at   END,
		       reviewed_at = CASE WHEN $2 = 'reviewed' THEN $3 ELSE reviewed_at END,
		       closed_at   = CASE WHEN $2 = 'closed'   THEN $3 ELSE closed_at   END
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, q, id, string(status), at)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AssessmentRepo) SaveColumnMapping(ctx context.Context, id int64, m *domain.ColumnMapping) error {
	if err := m.Validate(); err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("postgres: encode column_mapping: %w", err)
	}
	tag, err := r.db.q(ctx).Exec(ctx, `UPDATE assessments SET column_mapping = $2 WHERE id = $1`, id, b)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AssessmentRepo) SetCurrentRun(ctx context.Context, id int64, runID int64) error {
	tag, err := r.db.q(ctx).Exec(ctx, `UPDATE assessments SET current_run_id = $2 WHERE id = $1`, id, runID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *AssessmentRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.q(ctx).Exec(ctx, `DELETE FROM assessments WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// SaveUpload stores the original questionnaire file against the assessment.
func (r *AssessmentRepo) SaveUpload(ctx context.Context, u *domain.Upload) error {
	const q = `
		INSERT INTO assessment_uploads (assessment_id, filename, content_type, content, byte_size, sha256, sheet_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (assessment_id) DO UPDATE SET
			filename = EXCLUDED.filename,
			content_type = EXCLUDED.content_type,
			content = EXCLUDED.content,
			byte_size = EXCLUDED.byte_size,
			sha256 = EXCLUDED.sha256,
			sheet_name = EXCLUDED.sheet_name,
			uploaded_at = now()
		RETURNING uploaded_at`
	err := r.db.q(ctx).QueryRow(ctx, q, u.AssessmentID, u.Filename, u.ContentType,
		u.Content, u.ByteSize, u.SHA256, u.SheetName).Scan(&u.UploadedAt)
	return mapErr(err)
}

func (r *AssessmentRepo) GetUpload(ctx context.Context, assessmentID int64) (*domain.Upload, error) {
	const q = `
		SELECT assessment_id, filename, content_type, content, byte_size, sha256, sheet_name, uploaded_at
		  FROM assessment_uploads WHERE assessment_id = $1`
	var u domain.Upload
	err := r.db.q(ctx).QueryRow(ctx, q, assessmentID).Scan(
		&u.AssessmentID, &u.Filename, &u.ContentType, &u.Content,
		&u.ByteSize, &u.SHA256, &u.SheetName, &u.UploadedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (r *AssessmentRepo) SetUploadSheet(ctx context.Context, assessmentID int64, sheet string) error {
	_, err := r.db.q(ctx).Exec(ctx,
		`UPDATE assessment_uploads SET sheet_name = $2 WHERE assessment_id = $1`, assessmentID, sheet)
	return mapErr(err)
}

func (r *AssessmentRepo) DiscardUpload(ctx context.Context, assessmentID int64) error {
	_, err := r.db.q(ctx).Exec(ctx, `DELETE FROM assessment_uploads WHERE assessment_id = $1`, assessmentID)
	return mapErr(err)
}

var _ domain.AssessmentRepository = (*AssessmentRepo)(nil)
