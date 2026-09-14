package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"third-party-review/internal/domain"
)

// ReviewResultRepo is the PostgreSQL implementation of
// domain.ReviewResultRepository. Results are child rows keyed by run so a
// re-review never destroys what an earlier run concluded.
type ReviewResultRepo struct{ db *DB }

const resultCols = `
	id, question_id, run_id, risk_score, completeness, flags, rationale,
	feedback_draft, confidence, provider, model, raw, created_at`

func scanResult(s interface{ Scan(...any) error }) (*domain.ReviewResult, error) {
	var (
		res   domain.ReviewResult
		flags []byte
	)
	if err := s.Scan(&res.ID, &res.QuestionID, &res.RunID, &res.RiskScore,
		&res.Completeness, &flags, &res.Rationale, &res.FeedbackDraft,
		&res.Confidence, &res.Provider, &res.Model, &res.Raw, &res.CreatedAt); err != nil {
		return nil, mapErr(err)
	}
	if len(flags) > 0 {
		if err := json.Unmarshal(flags, &res.Flags); err != nil {
			return nil, fmt.Errorf("postgres: decode flags for result %d: %w", res.ID, err)
		}
	}
	return &res, nil
}

func (r *ReviewResultRepo) BulkCreate(ctx context.Context, results []*domain.ReviewResult) error {
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
		res.Normalize()
		flags, err := json.Marshal(res.Flags)
		if err != nil {
			return fmt.Errorf("postgres: encode flags: %w", err)
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

	rows, err := r.db.q(ctx).Query(ctx, sb.String(), args...)
	if err != nil {
		return mapErr(err)
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		if i >= len(results) {
			break
		}
		if err := rows.Scan(&results[i].ID, &results[i].CreatedAt); err != nil {
			return mapErr(err)
		}
		i++
	}
	return mapErr(rows.Err())
}

// LatestByAssessment returns the newest result per question. With runID set it
// is scoped to that run; with runID nil it takes the newest of any run, which
// is what the results view shows by default.
func (r *ReviewResultRepo) LatestByAssessment(ctx context.Context, assessmentID int64, runID *int64) (map[int64]*domain.ReviewResult, error) {
	stmt := `
		SELECT ` + resultCols + `
		  FROM (
		    SELECT rr.*, ROW_NUMBER() OVER (PARTITION BY rr.question_id ORDER BY rr.created_at DESC, rr.id DESC) AS rn
		      FROM review_results rr
		      JOIN questions q ON q.id = rr.question_id
		     WHERE q.assessment_id = $1
		       AND ($2::BIGINT IS NULL OR rr.run_id = $2)
		  ) ranked
		 WHERE rn = 1`
	rows, err := r.db.q(ctx).Query(ctx, stmt, assessmentID, runID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	out := map[int64]*domain.ReviewResult{}
	for rows.Next() {
		res, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out[res.QuestionID] = res
	}
	return out, mapErr(rows.Err())
}

func (r *ReviewResultRepo) HistoryByQuestion(ctx context.Context, questionID int64) ([]*domain.ReviewResult, error) {
	const stmt = `SELECT ` + resultCols + ` FROM review_results WHERE question_id = $1 ORDER BY created_at DESC, id DESC`
	rows, err := r.db.q(ctx).Query(ctx, stmt, questionID)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []*domain.ReviewResult
	for rows.Next() {
		res, err := scanResult(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, res)
	}
	return out, mapErr(rows.Err())
}

func (r *ReviewResultRepo) DeleteByAssessment(ctx context.Context, assessmentID int64) error {
	const stmt = `
		DELETE FROM review_results
		 WHERE question_id IN (SELECT id FROM questions WHERE assessment_id = $1)`
	_, err := r.db.q(ctx).Exec(ctx, stmt, assessmentID)
	return mapErr(err)
}

// truncate caps oversized raw model output so one pathological response cannot
// bloat the table.
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}

var _ domain.ReviewResultRepository = (*ReviewResultRepo)(nil)
