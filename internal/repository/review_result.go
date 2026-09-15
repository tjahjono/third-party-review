package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type ReviewResultRepository interface {
	BulkCreate(ctx context.Context, results []*model.ReviewResult) error
	LatestByAssessment(ctx context.Context, assessmentID uuid.UUID, runID *uuid.UUID) (map[uuid.UUID]*model.ReviewResult, error)
	HistoryByQuestion(ctx context.Context, questionID uuid.UUID) ([]*model.ReviewResult, error)
	DeleteByAssessment(ctx context.Context, assessmentID uuid.UUID) error
}

type reviewResultRepository struct {
	db helper.ConnProvider
}

func NewReviewResultRepository(db helper.ConnProvider) ReviewResultRepository {
	return &reviewResultRepository{db: db}
}

const resultColumns = `
	id, question_id, run_id, risk_score, completeness, flags, rationale,
	feedback_draft, confidence, provider, model, raw, created_at`

func scanResult(s interface{ Scan(...any) error }) (*model.ReviewResult, error) {
	var (
		res   model.ReviewResult
		flags []byte
	)
	if err := s.Scan(&res.ID, &res.QuestionID, &res.RunID, &res.RiskScore,
		&res.Completeness, &flags, &res.Rationale, &res.FeedbackDraft,
		&res.Confidence, &res.Provider, &res.Model, &res.Raw, &res.CreatedAt); err != nil {
		return nil, helper.MapErr(err)
	}
	if len(flags) > 0 {
		if err := json.Unmarshal(flags, &res.Flags); err != nil {
			return nil, fmt.Errorf("repository: decode flags for result %s: %w", res.ID, err)
		}
	}
	return &res, nil
}

func (r *reviewResultRepository) BulkCreate(ctx context.Context, results []*model.ReviewResult) error {
	if len(results) == 0 {
		return nil
	}
	var (
		sb   strings.Builder
		args = make([]any, 0, len(results)*11)
	)
	sb.WriteString(`INSERT INTO review_results (
		question_id, run_id, risk_score, completeness, flags, rationale,
		feedback_draft, confidence, provider, model, raw) VALUES `)
	for i, res := range results {
		helper.NormalizeResult(res)
		flags, err := json.Marshal(res.Flags)
		if err != nil {
			return fmt.Errorf("repository: encode flags: %w", err)
		}
		if res.Flags == nil {
			flags = []byte("[]")
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * 11
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6,
			base+7, base+8, base+9, base+10, base+11)
		args = append(args, res.QuestionID, res.RunID, int(res.RiskScore),
			string(res.Completeness), flags, res.Rationale, res.FeedbackDraft,
			res.Confidence, res.Provider, res.Model, truncate(res.Raw, 100_000))
	}
	// A run may be retried for a subset of questions; the newer result wins.
	sb.WriteString(` ON CONFLICT (question_id, run_id) DO UPDATE SET
		risk_score = EXCLUDED.risk_score,
		completeness = EXCLUDED.completeness,
		flags = EXCLUDED.flags,
		rationale = EXCLUDED.rationale,
		feedback_draft = EXCLUDED.feedback_draft,
		confidence = EXCLUDED.confidence,
		raw = EXCLUDED.raw,
		created_at = now()
		RETURNING id, created_at`)

	rows, err := r.db.Querier(ctx).Query(ctx, sb.String(), args...)
	if err != nil {
		return helper.MapErr(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		if i >= len(results) {
			break
		}
		if err := rows.Scan(&results[i].ID, &results[i].CreatedAt); err != nil {
			return helper.MapErr(err)
		}
		i++
	}
	return helper.MapErr(rows.Err())
}

// LatestByAssessment returns the newest result per question. With runID set it
// is scoped to that run; with runID nil it takes the newest of any run, which
// is what the results view shows by default.
func (r *reviewResultRepository) LatestByAssessment(ctx context.Context, assessmentID uuid.UUID, runID *uuid.UUID) (map[uuid.UUID]*model.ReviewResult, error) {
	stmt := `
		SELECT ` + resultColumns + `
		  FROM (
		    SELECT rr.*, ROW_NUMBER() OVER (PARTITION BY rr.question_id ORDER BY rr.created_at DESC, rr.id DESC) AS rn
		      FROM review_results rr
		      JOIN questions q ON q.id = rr.question_id
		     WHERE q.assessment_id = $1
		       AND ($2::UUID IS NULL OR rr.run_id = $2)
		  ) ranked
		 WHERE rn = 1`
	rows, err := r.db.Querier(ctx).Query(ctx, stmt, assessmentID, runID)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	out := map[uuid.UUID]*model.ReviewResult{}
	for rows.Next() {
		res, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out[res.QuestionID] = res
	}
	return out, helper.MapErr(rows.Err())
}

func (r *reviewResultRepository) HistoryByQuestion(ctx context.Context, questionID uuid.UUID) ([]*model.ReviewResult, error) {
	const stmt = `SELECT ` + resultColumns + ` FROM review_results WHERE question_id = $1 ORDER BY created_at DESC, id DESC`
	rows, err := r.db.Querier(ctx).Query(ctx, stmt, questionID)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.ReviewResult
	for rows.Next() {
		res, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *reviewResultRepository) DeleteByAssessment(ctx context.Context, assessmentID uuid.UUID) error {
	const stmt = `
		DELETE FROM review_results
		 WHERE question_id IN (SELECT id FROM questions WHERE assessment_id = $1)`
	_, err := r.db.Querier(ctx).Exec(ctx, stmt, assessmentID)
	return helper.MapErr(err)
}

// truncate caps oversized raw model output so one pathological response cannot
// bloat the table.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
