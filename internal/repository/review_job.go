package repository

import (
	"context"
	"time"

	"github.com/google/uuid"

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
}

type reviewJobRepository struct {
	db helper.ConnProvider
}

func NewReviewJobRepository(db helper.ConnProvider) ReviewJobRepository {
	return &reviewJobRepository{db: db}
}

const jobColumns = `
	id, assessment_id, status, total_questions, done_questions, failed_questions,
	stage, provider, model, rubric_id, error, created_at, started_at,
	finished_at, heartbeat_at`

func scanJob(s interface{ Scan(...any) error }) (*model.ReviewJob, error) {
	var j model.ReviewJob
	if err := s.Scan(&j.ID, &j.AssessmentID, &j.Status, &j.TotalQuestions,
		&j.DoneQuestions, &j.FailedQuestions, &j.Stage, &j.Provider, &j.Model,
		&j.RubricID, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
		&j.HeartbeatAt); err != nil {
		return nil, helper.MapErr(err)
	}
	return &j, nil
}

func (r *reviewJobRepository) Create(ctx context.Context, j *model.ReviewJob) error {
	const q = `
		INSERT INTO review_jobs (assessment_id, status, total_questions, stage, provider, model, rubric_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`
	if j.Status == "" {
		j.Status = model.JobQueued
	}
	err := r.db.Querier(ctx).QueryRow(ctx, q, j.AssessmentID, string(j.Status),
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
