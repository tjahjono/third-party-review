package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"third-party-review/internal/domain"
)

// QuestionRepo is the PostgreSQL implementation of domain.QuestionRepository.
type QuestionRepo struct{ db *DB }

const questionCols = `
	q.id, q.assessment_id, q.domain_id, q.source_row, q.position,
	q.question_text, q.assessor_remark, q.third_party_answer,
	q.third_party_remark, q.third_party_feedback, q.link_evidence,
	COALESCE(q.assessor_feedback_draft, ''), COALESCE(q.assessor_feedback_final, ''),
	q.review_status, q.finalized_at, q.finalized_by, q.created_at, q.updated_at`

func scanQuestion(s interface{ Scan(...any) error }, withDomain bool) (*domain.Question, error) {
	var q domain.Question
	targets := []any{
		&q.ID, &q.AssessmentID, &q.DomainID, &q.SourceRow, &q.Position,
		&q.QuestionText, &q.AssessorRemark, &q.ThirdPartyAnswer,
		&q.ThirdPartyRemark, &q.ThirdPartyFeedback, &q.LinkEvidence,
		&q.AssessorFeedbackDraft, &q.AssessorFeedbackFinal,
		&q.ReviewStatus, &q.FinalizedAt, &q.FinalizedBy, &q.CreatedAt, &q.UpdatedAt,
	}
	if withDomain {
		targets = append(targets, &q.DomainName, &q.ScrutinyNote)
	}
	if err := s.Scan(targets...); err != nil {
		return nil, mapErr(err)
	}
	return &q, nil
}

// BulkCreate inserts every question for an assessment in one round trip and
// writes the assigned IDs back onto the supplied slice. Ingestion of a
// hundred-row questionnaire is therefore a single statement, not a hundred.
func (r *QuestionRepo) BulkCreate(ctx context.Context, questions []*domain.Question) error {
	if len(questions) == 0 {
		return nil
	}
	var (
		sb   strings.Builder
		args = make([]any, 0, len(questions)*11)
	)
	sb.WriteString(`INSERT INTO questions (
		assessment_id, domain_id, source_row, position, question_text,
		assessor_remark, third_party_answer, third_party_remark,
		third_party_feedback, link_evidence, review_status) VALUES `)
	for i, q := range questions {
		if err := q.Validate(); err != nil {
			return fmt.Errorf("question at source row %d: %w", q.SourceRow+1, err)
		}
		if i > 0 {
			sb.WriteString(", ")
		}
		base := i * 11
		fmt.Fprintf(&sb, "($%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			base+1, base+2, base+3, base+4, base+5, base+6,
			base+7, base+8, base+9, base+10, base+11)
		args = append(args,
			q.AssessmentID, q.DomainID, q.SourceRow, q.Position, q.QuestionText,
			q.AssessorRemark, q.ThirdPartyAnswer, q.ThirdPartyRemark,
			q.ThirdPartyFeedback, q.LinkEvidence, string(q.ReviewStatus))
	}
	sb.WriteString(" RETURNING id, created_at, updated_at")

	rows, err := r.db.q(ctx).Query(ctx, sb.String(), args...)
	if err != nil {
		return mapErr(err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		if i >= len(questions) {
			break
		}
		if err := rows.Scan(&questions[i].ID, &questions[i].CreatedAt, &questions[i].UpdatedAt); err != nil {
			return mapErr(err)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return mapErr(err)
	}
	if i != len(questions) {
		return fmt.Errorf("postgres: inserted %d questions but expected %d", i, len(questions))
	}
	return nil
}

func (r *QuestionRepo) Update(ctx context.Context, q *domain.Question) error {
	if err := q.Validate(); err != nil {
		return err
	}
	const stmt = `
		UPDATE questions
		   SET domain_id = $2, question_text = $3, assessor_remark = $4,
		       third_party_answer = $5, third_party_remark = $6,
		       third_party_feedback = $7, link_evidence = $8
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.q(ctx).QueryRow(ctx, stmt, q.ID, q.DomainID, q.QuestionText,
		q.AssessorRemark, q.ThirdPartyAnswer, q.ThirdPartyRemark,
		q.ThirdPartyFeedback, q.LinkEvidence).Scan(&q.UpdatedAt)
	return mapErr(err)
}

func (r *QuestionRepo) GetByID(ctx context.Context, id int64) (*domain.Question, error) {
	const stmt = `
		SELECT ` + questionCols + `, d.name, d.scrutiny_note
		  FROM questions q
		  JOIN assessment_domains d ON d.id = q.domain_id
		 WHERE q.id = $1`
	return scanQuestion(r.db.q(ctx).QueryRow(ctx, stmt, id), true)
}

func (r *QuestionRepo) List(ctx context.Context, f domain.QuestionFilter) ([]*domain.Question, error) {
	args := []any{f.AssessmentID}
	clauses := []string{"q.assessment_id = $1"}
	if f.DomainID != nil {
		args = append(args, *f.DomainID)
		clauses = append(clauses, fmt.Sprintf("q.domain_id = $%d", len(args)))
	}
	if f.ReviewStatus != nil {
		args = append(args, string(*f.ReviewStatus))
		clauses = append(clauses, fmt.Sprintf("q.review_status = $%d", len(args)))
	}
	// Flag and score filters look at the newest result for each question.
	if f.FlaggedOnly {
		clauses = append(clauses, `EXISTS (
			SELECT 1 FROM review_results rr
			 WHERE rr.question_id = q.id
			   AND jsonb_array_length(rr.flags) > 0
			 ORDER BY rr.created_at DESC LIMIT 1)`)
	}
	if f.MinRiskScore != nil {
		args = append(args, *f.MinRiskScore)
		clauses = append(clauses, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM review_results rr
			 WHERE rr.question_id = q.id AND rr.risk_score >= $%d)`, len(args)))
	}

	stmt := fmt.Sprintf(`
		SELECT %s, d.name, d.scrutiny_note
		  FROM questions q
		  JOIN assessment_domains d ON d.id = q.domain_id
		 WHERE %s
		 ORDER BY d.sort_order, q.position, q.id`,
		questionCols, strings.Join(clauses, " AND "))

	rows, err := r.db.q(ctx).Query(ctx, stmt, args...)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []*domain.Question
	for rows.Next() {
		q, err := scanQuestion(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, mapErr(rows.Err())
}

func (r *QuestionRepo) ListForReview(ctx context.Context, assessmentID int64) ([]*domain.Question, error) {
	return r.List(ctx, domain.QuestionFilter{AssessmentID: assessmentID})
}

func (r *QuestionRepo) CountByAssessment(ctx context.Context, assessmentID int64) (int, error) {
	var n int
	err := r.db.q(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM questions WHERE assessment_id = $1`, assessmentID).Scan(&n)
	return n, mapErr(err)
}

func (r *QuestionRepo) SetDomain(ctx context.Context, questionID, domainID int64) error {
	tag, err := r.db.q(ctx).Exec(ctx, `UPDATE questions SET domain_id = $2 WHERE id = $1`, questionID, domainID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ApplyDraft writes the AI draft and moves the question to ai_drafted. A
// question a human has already finalized is left alone: re-running a review
// must not silently reopen signed-off work.
func (r *QuestionRepo) ApplyDraft(ctx context.Context, questionID int64, draft string) error {
	const stmt = `
		UPDATE questions
		   SET assessor_feedback_draft = $2,
		       review_status = CASE WHEN review_status = 'finalized'
		                            THEN review_status ELSE 'ai_drafted' END
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, stmt, questionID, draft)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Finalize records human-signed-off feedback. assessor_feedback_draft is
// deliberately untouched so the AI's original wording is always recoverable.
func (r *QuestionRepo) Finalize(ctx context.Context, questionID int64, final string, userID *int64, at time.Time) error {
	if strings.TrimSpace(final) == "" {
		return domain.ValidationError{Field: "assessor_feedback_final", Message: "Feedback cannot be empty when finalizing."}
	}
	const stmt = `
		UPDATE questions
		   SET assessor_feedback_final = $2,
		       review_status = 'finalized',
		       finalized_at = $3,
		       finalized_by = $4
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, stmt, questionID, final, at, userID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Unfinalize reopens a question. The final text is kept so an accidental
// reopen loses nothing.
func (r *QuestionRepo) Unfinalize(ctx context.Context, questionID int64) error {
	const stmt = `
		UPDATE questions
		   SET review_status = CASE WHEN COALESCE(assessor_feedback_draft, '') = ''
		                            THEN 'pending' ELSE 'ai_drafted' END,
		       finalized_at = NULL,
		       finalized_by = NULL
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, stmt, questionID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *QuestionRepo) DeleteByAssessment(ctx context.Context, assessmentID int64) error {
	_, err := r.db.q(ctx).Exec(ctx, `DELETE FROM questions WHERE assessment_id = $1`, assessmentID)
	return mapErr(err)
}

var _ domain.QuestionRepository = (*QuestionRepo)(nil)
