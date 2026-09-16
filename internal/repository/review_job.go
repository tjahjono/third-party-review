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

type ReviewJobRepository interface {
	Create(ctx context.Context, j *model.ReviewJob) error
	GetByID(ctx context.Context, id uuid.UUID) (*model.ReviewJob, error)
	LatestByAssessment(ctx context.Context, assessmentID uuid.UUID) (*model.ReviewJob, error)
	ClaimNext(ctx context.Context) (*model.ReviewJob, error)
	UpdateProgress(ctx context.Context, id uuid.UUID, done, failed int, stage string) error
	Heartbeat(ctx context.Context, id uuid.UUID) error
	Finish(ctx context.Context, id uuid.UUID, status model.JobStatus, errMsg string) error
	ReclaimStalled(ctx context.Context, olderThan time.Duration) (int, error)
	HasActive(ctx context.Context, assessmentID uuid.UUID) (bool, error)
	// SaveQuestionScope records which questions a "selected"-scope job covers.
	SaveQuestionScope(ctx context.Context, jobID uuid.UUID, questionIDs []uuid.UUID) error
	// QuestionIDsForJob reads back a "selected"-scope job's question set.
	QuestionIDsForJob(ctx context.Context, jobID uuid.UUID) ([]uuid.UUID, error)
	// ListActive returns every queued or running job across all assessments,
	// with enough context to show on the dashboard without a query per row.
	ListActive(ctx context.Context) ([]dto.ActiveReview, error)
}

type reviewJobRepository struct {
	db helper.ConnProvider
}

func NewReviewJobRepository(db helper.ConnProvider) ReviewJobRepository {
	return &reviewJobRepository{db: db}
}

const jobColumns = `
	id, assessment_id, status, scope, total_questions, done_questions, failed_questions,
	stage, provider, model, rubric_id, error, created_at, started_at,
	finished_at, heartbeat_at`

func scanJob(s interface{ Scan(...any) error }) (*model.ReviewJob, error) {
	var j model.ReviewJob
	if err := s.Scan(&j.ID, &j.AssessmentID, &j.Status, &j.Scope, &j.TotalQuestions,
		&j.DoneQuestions, &j.FailedQuestions, &j.Stage, &j.Provider, &j.Model,
		&j.RubricID, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
		&j.HeartbeatAt); err != nil {
		return nil, helper.MapErr(err)
	}
	return &j, nil
}

func (r *reviewJobRepository) Create(ctx context.Context, j *model.ReviewJob) error {
	const q = `
		INSERT INTO review_jobs (assessment_id, status, scope, total_questions, stage, provider, model, rubric_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at`
	if j.Status == "" {
		j.Status = model.JobQueued
	}
	if j.Scope == "" {
		j.Scope = model.ScopeAll
	}
	err := r.db.Querier(ctx).QueryRow(ctx, q, j.AssessmentID, string(j.Status), string(j.Scope),
		j.TotalQuestions, j.Stage, j.Provider, j.Model, j.RubricID).
		Scan(&j.ID, &j.CreatedAt)
	return helper.MapErr(err)
}

func (r *reviewJobRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.ReviewJob, error) {
	return scanJob(r.db.Querier(ctx).QueryRow(ctx, `SELECT `+jobColumns+` FROM review_jobs WHERE id = $1`, id))
}

func (r *reviewJobRepository) LatestByAssessment(ctx context.Context, assessmentID uuid.UUID) (*model.ReviewJob, error) {
	const q = `SELECT ` + jobColumns + ` FROM review_jobs WHERE assessment_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`
	return scanJob(r.db.Querier(ctx).QueryRow(ctx, q, assessmentID))
}

// ClaimNext atomically moves the oldest queued job to running. SKIP LOCKED
// means several worker goroutines (or several app instances) can share the
// queue without claiming the same job twice.
func (r *reviewJobRepository) ClaimNext(ctx context.Context) (*model.ReviewJob, error) {
	const q = `
		UPDATE review_jobs
		   SET status = 'running', started_at = now(), heartbeat_at = now()
		 WHERE id = (
		     SELECT id FROM review_jobs
		      WHERE status = 'queued'
		      ORDER BY created_at
		      FOR UPDATE SKIP LOCKED
		      LIMIT 1
		 )
		RETURNING ` + jobColumns
	return scanJob(r.db.Querier(ctx).QueryRow(ctx, q))
}

func (r *reviewJobRepository) UpdateProgress(ctx context.Context, id uuid.UUID, done, failed int, stage string) error {
	const q = `
		UPDATE review_jobs
		   SET done_questions = $2, failed_questions = $3, stage = $4, heartbeat_at = now()
		 WHERE id = $1`
	tag, err := r.db.Querier(ctx).Exec(ctx, q, id, done, failed, stage)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

func (r *reviewJobRepository) Heartbeat(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `UPDATE review_jobs SET heartbeat_at = now() WHERE id = $1`, id)
	return helper.MapErr(err)
}

func (r *reviewJobRepository) Finish(ctx context.Context, id uuid.UUID, status model.JobStatus, errMsg string) error {
	if !helper.TerminalJob(status) {
		return helper.ValidationError{Field: "status", Message: "Finish requires a terminal job status."}
	}
	const q = `
		UPDATE review_jobs
		   SET status = $2, error = $3, finished_at = now(), heartbeat_at = now()
		 WHERE id = $1`
	tag, err := r.db.Querier(ctx).Exec(ctx, q, id, string(status), truncate(errMsg, 4000))
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}

// ReclaimStalled re-queues running jobs whose worker stopped heartbeating,
// which happens when the process restarts mid-run. Without this an assessment
// sits in `reviewing` forever with a progress bar that never moves.
func (r *reviewJobRepository) ReclaimStalled(ctx context.Context, olderThan time.Duration) (int, error) {
	const q = `
		UPDATE review_jobs
		   SET status = 'queued', started_at = NULL, heartbeat_at = NULL,
		       stage = 'Requeued after worker restart'
		 WHERE status = 'running'
		   AND COALESCE(heartbeat_at, started_at, created_at) < now() - $1::interval`
	tag, err := r.db.Querier(ctx).Exec(ctx, q, olderThan.String())
	if err != nil {
		return 0, helper.MapErr(err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *reviewJobRepository) HasActive(ctx context.Context, assessmentID uuid.UUID) (bool, error) {
	const q = `SELECT EXISTS (
		SELECT 1 FROM review_jobs
		 WHERE assessment_id = $1 AND status IN ('queued','running'))`
	var exists bool
	err := r.db.Querier(ctx).QueryRow(ctx, q, assessmentID).Scan(&exists)
	return exists, helper.MapErr(err)
}

// SaveQuestionScope records the question set a "selected"-scope job covers.
// A no-op for an empty slice: Enqueue never creates a selected-scope job with
// no questions, but a zero-row insert would otherwise be a confusing error.
func (r *reviewJobRepository) SaveQuestionScope(ctx context.Context, jobID uuid.UUID, questionIDs []uuid.UUID) error {
	if len(questionIDs) == 0 {
		return nil
	}
	var sb strings.Builder
	args := make([]any, 0, len(questionIDs)*2)
	sb.WriteString(`INSERT INTO review_job_questions (job_id, question_id) VALUES `)
	for i, qid := range questionIDs {
		if i > 0 {
			sb.WriteString(", ")
		}
		args = append(args, jobID, qid)
		fmt.Fprintf(&sb, "($%d,$%d)", len(args)-1, len(args))
	}
	sb.WriteString(` ON CONFLICT DO NOTHING`)
	_, err := r.db.Querier(ctx).Exec(ctx, sb.String(), args...)
	return helper.MapErr(err)
}

// QuestionIDsForJob reads back a "selected"-scope job's question set. The
// worker calls this rather than trusting anything held in memory, since the
// job it runs is always a fresh row read from the database.
func (r *reviewJobRepository) QuestionIDsForJob(ctx context.Context, jobID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := r.db.Querier(ctx).Query(ctx,
		`SELECT question_id FROM review_job_questions WHERE job_id = $1`, jobID)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, helper.MapErr(err)
		}
		out = append(out, id)
	}
	return out, helper.MapErr(rows.Err())
}

// ListActive returns every queued or running job, joined with the
// assessment/vendor context the dashboard shows alongside its progress bar.
func (r *reviewJobRepository) ListActive(ctx context.Context) ([]dto.ActiveReview, error) {
	const q = `
		SELECT j.id, j.assessment_id, j.status, j.scope, j.total_questions, j.done_questions,
		       j.failed_questions, j.stage, j.provider, j.model, j.rubric_id, j.error,
		       j.created_at, j.started_at, j.finished_at, j.heartbeat_at,
		       a.title, v.name
		  FROM review_jobs j
		  JOIN assessments a ON a.id = j.assessment_id
		  JOIN vendors v ON v.id = a.vendor_id
		 WHERE j.status IN ('queued','running')
		 ORDER BY j.created_at`
	rows, err := r.db.Querier(ctx).Query(ctx, q)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []dto.ActiveReview
	for rows.Next() {
		var (
			j                 model.ReviewJob
			title, vendorName string
		)
		if err := rows.Scan(&j.ID, &j.AssessmentID, &j.Status, &j.Scope, &j.TotalQuestions,
			&j.DoneQuestions, &j.FailedQuestions, &j.Stage, &j.Provider, &j.Model,
			&j.RubricID, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
			&j.HeartbeatAt, &title, &vendorName); err != nil {
			return nil, helper.MapErr(err)
		}
		out = append(out, dto.ActiveReview{
			Job:             &j,
			AssessmentID:    j.AssessmentID,
			AssessmentTitle: title,
			VendorName:      vendorName,
		})
	}
	return out, helper.MapErr(rows.Err())
}
