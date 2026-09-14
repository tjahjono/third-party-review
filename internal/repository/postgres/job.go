package postgres

import (
	"context"
	"time"

	"third-party-review/internal/domain"
)

// JobRepo is the PostgreSQL implementation of domain.JobRepository. The queue
// is a table rather than an external broker: this is one internal team's
// workload, and a table keeps the deployment to app + Postgres.
type JobRepo struct{ db *DB }

const jobCols = `
	id, assessment_id, status, total_questions, done_questions, failed_questions,
	stage, provider, model, rubric_id, error, created_at, started_at,
	finished_at, heartbeat_at`

func scanJob(s interface{ Scan(...any) error }) (*domain.ReviewJob, error) {
	var j domain.ReviewJob
	if err := s.Scan(&j.ID, &j.AssessmentID, &j.Status, &j.TotalQuestions,
		&j.DoneQuestions, &j.FailedQuestions, &j.Stage, &j.Provider, &j.Model,
		&j.RubricID, &j.Error, &j.CreatedAt, &j.StartedAt, &j.FinishedAt,
		&j.HeartbeatAt); err != nil {
		return nil, mapErr(err)
	}
	return &j, nil
}

func (r *JobRepo) Create(ctx context.Context, j *domain.ReviewJob) error {
	const q = `
		INSERT INTO review_jobs (assessment_id, status, total_questions, stage, provider, model, rubric_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`
	if j.Status == "" {
		j.Status = domain.JobQueued
	}
	err := r.db.q(ctx).QueryRow(ctx, q, j.AssessmentID, string(j.Status),
		j.TotalQuestions, j.Stage, j.Provider, j.Model, j.RubricID).
		Scan(&j.ID, &j.CreatedAt)
	return mapErr(err)
}

func (r *JobRepo) GetByID(ctx context.Context, id int64) (*domain.ReviewJob, error) {
	return scanJob(r.db.q(ctx).QueryRow(ctx, `SELECT `+jobCols+` FROM review_jobs WHERE id = $1`, id))
}

func (r *JobRepo) LatestByAssessment(ctx context.Context, assessmentID int64) (*domain.ReviewJob, error) {
	const q = `SELECT ` + jobCols + ` FROM review_jobs WHERE assessment_id = $1 ORDER BY created_at DESC, id DESC LIMIT 1`
	return scanJob(r.db.q(ctx).QueryRow(ctx, q, assessmentID))
}

// ClaimNext atomically moves the oldest queued job to running. SKIP LOCKED
// means several worker goroutines (or several app instances) can share the
// queue without claiming the same job twice.
func (r *JobRepo) ClaimNext(ctx context.Context) (*domain.ReviewJob, error) {
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
		RETURNING ` + jobCols
	return scanJob(r.db.q(ctx).QueryRow(ctx, q))
}

func (r *JobRepo) UpdateProgress(ctx context.Context, id int64, done, failed int, stage string) error {
	const q = `
		UPDATE review_jobs
		   SET done_questions = $2, failed_questions = $3, stage = $4, heartbeat_at = now()
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, q, id, done, failed, stage)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *JobRepo) Heartbeat(ctx context.Context, id int64) error {
	_, err := r.db.q(ctx).Exec(ctx, `UPDATE review_jobs SET heartbeat_at = now() WHERE id = $1`, id)
	return mapErr(err)
}

func (r *JobRepo) Finish(ctx context.Context, id int64, status domain.JobStatus, errMsg string) error {
	if !status.Terminal() {
		return domain.ValidationError{Field: "status", Message: "Finish requires a terminal job status."}
	}
	const q = `
		UPDATE review_jobs
		   SET status = $2, error = $3, finished_at = now(), heartbeat_at = now()
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, q, id, string(status), truncate(errMsg, 4000))
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ReclaimStalled re-queues running jobs whose worker stopped heartbeating,
// which happens when the process restarts mid-run. Without this an assessment
// sits in `reviewing` forever with a progress bar that never moves.
func (r *JobRepo) ReclaimStalled(ctx context.Context, olderThan time.Duration) (int, error) {
	const q = `
		UPDATE review_jobs
		   SET status = 'queued', started_at = NULL, heartbeat_at = NULL,
		       stage = 'Requeued after worker restart'
		 WHERE status = 'running'
		   AND COALESCE(heartbeat_at, started_at, created_at) < now() - $1::interval`
	tag, err := r.db.q(ctx).Exec(ctx, q, olderThan.String())
	if err != nil {
		return 0, mapErr(err)
	}
	return int(tag.RowsAffected()), nil
}

func (r *JobRepo) HasActive(ctx context.Context, assessmentID int64) (bool, error) {
	const q = `SELECT EXISTS (
		SELECT 1 FROM review_jobs
		 WHERE assessment_id = $1 AND status IN ('queued','running'))`
	var exists bool
	err := r.db.q(ctx).QueryRow(ctx, q, assessmentID).Scan(&exists)
	return exists, mapErr(err)
}

var _ domain.JobRepository = (*JobRepo)(nil)
