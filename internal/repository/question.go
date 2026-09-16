package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"third-party-review/internal/dto"
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type QuestionRepository interface {
	BulkCreate(ctx context.Context, questions []*model.Question) error
	Update(ctx context.Context, q *model.Question) error
	GetByID(ctx context.Context, id uuid.UUID) (*model.Question, error)
	List(ctx context.Context, f dto.QuestionFilter) ([]*model.Question, error)
	ListForReview(ctx context.Context, assessmentID uuid.UUID) ([]*model.Question, error)
	// ListByIDs fetches a specific subset of an assessment's questions, scoped
	// to that assessment so a stray id from elsewhere cannot be reviewed.
	ListByIDs(ctx context.Context, assessmentID uuid.UUID, ids []uuid.UUID) ([]*model.Question, error)
	CountByAssessment(ctx context.Context, assessmentID uuid.UUID) (int, error)
	SetDomain(ctx context.Context, questionID, domainID uuid.UUID) error
	ApplyDraft(ctx context.Context, questionID uuid.UUID, draft string) error
	Finalize(ctx context.Context, questionID uuid.UUID, final string, userID *uuid.UUID, at time.Time) error
	Unfinalize(ctx context.Context, questionID uuid.UUID) error
	DeleteByAssessment(ctx context.Context, assessmentID uuid.UUID) error
}

type questionRepository struct {
	db helper.ConnProvider
}

func NewQuestionRepository(db helper.ConnProvider) QuestionRepository {
	return &questionRepository{db: db}
}

const questionColumns = `
	q.id, q.assessment_id, q.domain_id, q.source_row, q.position,
	q.question_text, q.assessor_remark, q.third_party_answer,
	q.third_party_remark, q.third_party_feedback, q.link_evidence,
	COALESCE(q.assessor_feedback_draft, ''), COALESCE(q.assessor_feedback_final, ''),
	q.review_status, q.finalized_at, q.finalized_by, q.created_at, q.updated_at`

func scanQuestion(s interface{ Scan(...any) error }, withDomain bool) (*model.Question, error) {
	var q model.Question
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
		return nil, helper.MapErr(err)
	}
	return &q, nil
}

// BulkCreate inserts every question for an assessment in one round trip and
// writes the assigned IDs back onto the supplied slice. Ingestion of a
// hundred-row questionnaire is therefore a single statement, not a hundred.
func (r *questionRepository) BulkCreate(ctx context.Context, questions []*model.Question) error {
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

	rows, err := r.db.Querier(ctx).Query(ctx, sb.String(), args...)
	if err != nil {
		return helper.MapErr(err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		if i >= len(questions) {
			break
		}
		if err := rows.Scan(&questions[i].ID, &questions[i].CreatedAt, &questions[i].UpdatedAt); err != nil {
			return helper.MapErr(err)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		return helper.MapErr(err)
	}
	if i != len(questions) {
		return fmt.Errorf("repository: inserted %d questions but expected %d", i, len(questions))
	}
	return nil
}

func (r *questionRepository) Update(ctx context.Context, q *model.Question) error {
	const stmt = `
		UPDATE questions
		   SET domain_id = $2, question_text = $3, assessor_remark = $4,
		       third_party_answer = $5, third_party_remark = $6,
		       third_party_feedback = $7, link_evidence = $8
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, stmt, q.ID, q.DomainID, q.QuestionText,
		q.AssessorRemark, q.ThirdPartyAnswer, q.ThirdPartyRemark,
		q.ThirdPartyFeedback, q.LinkEvidence).Scan(&q.UpdatedAt)
	return helper.MapErr(err)
}

func (r *questionRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.Question, error) {
	const stmt = `
		SELECT ` + questionColumns + `, d.name, d.scrutiny_note
		  FROM questions q
		  JOIN assessment_domains d ON d.id = q.domain_id
		 WHERE q.id = $1`
	return scanQuestion(r.db.Querier(ctx).QueryRow(ctx, stmt, id), true)
}

func (r *questionRepository) List(ctx context.Context, f dto.QuestionFilter) ([]*model.Question, error) {
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
	// Flag, concern and score filters all look at the same row: the newest
	// result for the question, exactly what Aggregate scores from - so a
	// reviewer filtering here always sees the same population the summary
	// panel counted them under (see model.RiskFlagThreshold).
	const latestResult = `(
		SELECT rr.flags, rr.risk_score
		  FROM review_results rr
		 WHERE rr.question_id = q.id
		 ORDER BY rr.created_at DESC, rr.id DESC
		 LIMIT 1)`
	if f.FlaggedOnly {
		clauses = append(clauses, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM %s latest
			 WHERE jsonb_array_length(latest.flags) > 0 OR latest.risk_score >= %d)`,
			latestResult, int(model.RiskFlagThreshold)))
	}
	if f.NoConcernOnly {
		// A question with no result yet, or one the model returned an
		// unparseable/absent score for (risk_score 0), is neither flagged nor
		// clean - it just hasn't been meaningfully reviewed, so it must not
		// show up here as if the AI had looked and found nothing wrong.
		clauses = append(clauses, fmt.Sprintf(`EXISTS (
			SELECT 1 FROM %s latest
			 WHERE jsonb_array_length(latest.flags) = 0
			   AND latest.risk_score BETWEEN %d AND %d)`,
			latestResult, int(model.RiskMin), int(model.RiskFlagThreshold)-1))
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
		questionColumns, strings.Join(clauses, " AND "))

	rows, err := r.db.Querier(ctx).Query(ctx, stmt, args...)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.Question
	for rows.Next() {
		q, err := scanQuestion(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *questionRepository) ListForReview(ctx context.Context, assessmentID uuid.UUID) ([]*model.Question, error) {
	return r.List(ctx, dto.QuestionFilter{AssessmentID: assessmentID})
}

// ListByIDs fetches the given questions, filtered to those belonging to
// assessmentID. Placeholders are built explicitly (rather than relying on a
// driver-level array parameter) to match how BulkCreate builds its own
// multi-row statement above.
func (r *questionRepository) ListByIDs(ctx context.Context, assessmentID uuid.UUID, ids []uuid.UUID) ([]*model.Question, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, assessmentID)
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		args = append(args, id)
		placeholders[i] = fmt.Sprintf("$%d", len(args))
	}

	stmt := fmt.Sprintf(`
		SELECT %s, d.name, d.scrutiny_note
		  FROM questions q
		  JOIN assessment_domains d ON d.id = q.domain_id
		 WHERE q.assessment_id = $1 AND q.id IN (%s)
		 ORDER BY d.sort_order, q.position, q.id`,
		questionColumns, strings.Join(placeholders, ","))

	rows, err := r.db.Querier(ctx).Query(ctx, stmt, args...)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.Question
	for rows.Next() {
		q, err := scanQuestion(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *questionRepository) CountByAssessment(ctx context.Context, assessmentID uuid.UUID) (int, error) {
	var n int
	err := r.db.Querier(ctx).QueryRow(ctx, `SELECT COUNT(*) FROM questions WHERE assessment_id = $1`, assessmentID).Scan(&n)
	return n, helper.MapErr(err)
}

func (r *questionRepository) SetDomain(ctx context.Context, questionID, domainID uuid.UUID) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `UPDATE questions SET domain_id = $2 WHERE id = $1`, questionID, domainID)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

// ApplyDraft writes the AI draft and moves the question to ai_drafted. A
// question a human has already finalized is left alone: re-running a review
// must not silently reopen signed-off work.
func (r *questionRepository) ApplyDraft(ctx context.Context, questionID uuid.UUID, draft string) error {
	const stmt = `
		UPDATE questions
		   SET assessor_feedback_draft = $2,
		       review_status = CASE WHEN review_status = 'finalized'
		                            THEN review_status ELSE 'ai_drafted' END
		 WHERE id = $1`
	tag, err := r.db.Querier(ctx).Exec(ctx, stmt, questionID, draft)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

// Finalize records human-signed-off feedback. assessor_feedback_draft is
// deliberately untouched so the AI's original wording is always recoverable.
func (r *questionRepository) Finalize(ctx context.Context, questionID uuid.UUID, final string, userID *uuid.UUID, at time.Time) error {
	if strings.TrimSpace(final) == "" {
		return helper.ValidationError{Field: "assessor_feedback_final", Message: "Feedback cannot be empty when finalizing."}
	}
	const stmt = `
		UPDATE questions
		   SET assessor_feedback_final = $2,
		       review_status = 'finalized',
		       finalized_at = $3,
		       finalized_by = $4
		 WHERE id = $1`
	tag, err := r.db.Querier(ctx).Exec(ctx, stmt, questionID, final, at, userID)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

// Unfinalize reopens a question. The final text is kept so an accidental
// reopen loses nothing.
func (r *questionRepository) Unfinalize(ctx context.Context, questionID uuid.UUID) error {
	const stmt = `
		UPDATE questions
		   SET review_status = CASE WHEN COALESCE(assessor_feedback_draft, '') = ''
		                            THEN 'pending' ELSE 'ai_drafted' END,
		       finalized_at = NULL,
		       finalized_by = NULL
		 WHERE id = $1`
	tag, err := r.db.Querier(ctx).Exec(ctx, stmt, questionID)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

func (r *questionRepository) DeleteByAssessment(ctx context.Context, assessmentID uuid.UUID) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM questions WHERE assessment_id = $1`, assessmentID)
	return helper.MapErr(err)
}
