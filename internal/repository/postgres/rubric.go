package postgres

import (
	"context"

	"third-party-review/internal/domain"
)

// RubricRepo is the PostgreSQL implementation of domain.RubricRepository.
type RubricRepo struct{ db *DB }

const rubricCols = `id, name, content, reusable, created_at, updated_at`

func (r *RubricRepo) Create(ctx context.Context, rb *domain.Rubric) error {
	if err := rb.Validate(); err != nil {
		return err
	}
	const q = `INSERT INTO rubrics (name, content, reusable) VALUES ($1,$2,$3)
	           RETURNING id, created_at, updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, rb.Name, rb.Content, rb.Reusable).
		Scan(&rb.ID, &rb.CreatedAt, &rb.UpdatedAt)
	return mapErr(err)
}

func (r *RubricRepo) Update(ctx context.Context, rb *domain.Rubric) error {
	if err := rb.Validate(); err != nil {
		return err
	}
	const q = `UPDATE rubrics SET name=$2, content=$3, reusable=$4 WHERE id=$1 RETURNING updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, rb.ID, rb.Name, rb.Content, rb.Reusable).Scan(&rb.UpdatedAt)
	return mapErr(err)
}

func (r *RubricRepo) GetByID(ctx context.Context, id int64) (*domain.Rubric, error) {
	const q = `SELECT ` + rubricCols + ` FROM rubrics WHERE id = $1`
	var rb domain.Rubric
	err := r.db.q(ctx).QueryRow(ctx, q, id).Scan(&rb.ID, &rb.Name, &rb.Content, &rb.Reusable, &rb.CreatedAt, &rb.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &rb, nil
}

func (r *RubricRepo) ListReusable(ctx context.Context) ([]*domain.Rubric, error) {
	const q = `SELECT ` + rubricCols + ` FROM rubrics WHERE reusable ORDER BY name`
	rows, err := r.db.q(ctx).Query(ctx, q)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []*domain.Rubric
	for rows.Next() {
		var rb domain.Rubric
		if err := rows.Scan(&rb.ID, &rb.Name, &rb.Content, &rb.Reusable, &rb.CreatedAt, &rb.UpdatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, &rb)
	}
	return out, mapErr(rows.Err())
}

func (r *RubricRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.q(ctx).Exec(ctx, `DELETE FROM rubrics WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// AttachToAssessment binds a rubric to an assessment. One rubric per
// assessment; attaching a second replaces the first.
func (r *RubricRepo) AttachToAssessment(ctx context.Context, assessmentID, rubricID int64) error {
	const q = `
		INSERT INTO assessment_rubrics (assessment_id, rubric_id)
		VALUES ($1, $2)
		ON CONFLICT (assessment_id) DO UPDATE SET rubric_id = EXCLUDED.rubric_id, attached_at = now()`
	_, err := r.db.q(ctx).Exec(ctx, q, assessmentID, rubricID)
	return mapErr(err)
}

func (r *RubricRepo) DetachFromAssessment(ctx context.Context, assessmentID int64) error {
	_, err := r.db.q(ctx).Exec(ctx, `DELETE FROM assessment_rubrics WHERE assessment_id = $1`, assessmentID)
	return mapErr(err)
}

// GetForAssessment returns the attached rubric, or domain.ErrNotFound when the
// assessment has none. Callers treat ErrNotFound as "review without a rubric".
func (r *RubricRepo) GetForAssessment(ctx context.Context, assessmentID int64) (*domain.Rubric, error) {
	const q = `
		SELECT r.id, r.name, r.content, r.reusable, r.created_at, r.updated_at
		  FROM assessment_rubrics ar
		  JOIN rubrics r ON r.id = ar.rubric_id
		 WHERE ar.assessment_id = $1`
	var rb domain.Rubric
	err := r.db.q(ctx).QueryRow(ctx, q, assessmentID).
		Scan(&rb.ID, &rb.Name, &rb.Content, &rb.Reusable, &rb.CreatedAt, &rb.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &rb, nil
}

var _ domain.RubricRepository = (*RubricRepo)(nil)
