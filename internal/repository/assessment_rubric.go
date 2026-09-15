package repository

import (
	"context"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type AssessmentRubricRepository interface {
	Attach(ctx context.Context, assessmentID, rubricID uuid.UUID) error
	Detach(ctx context.Context, assessmentID uuid.UUID) error
	GetRubric(ctx context.Context, assessmentID uuid.UUID) (*model.Rubric, error)
}

type assessmentRubricRepository struct {
	db helper.ConnProvider
}

func NewAssessmentRubricRepository(db helper.ConnProvider) AssessmentRubricRepository {
	return &assessmentRubricRepository{db: db}
}

// Attach binds a rubric to an assessment. One rubric per assessment;
// attaching a second replaces the first.
func (r *assessmentRubricRepository) Attach(ctx context.Context, assessmentID, rubricID uuid.UUID) error {
	const q = `
		INSERT INTO assessment_rubrics (assessment_id, rubric_id)
		VALUES ($1, $2)
		ON CONFLICT (assessment_id) DO UPDATE SET rubric_id = EXCLUDED.rubric_id, attached_at = now()`
	_, err := r.db.Querier(ctx).Exec(ctx, q, assessmentID, rubricID)
	return helper.MapErr(err)
}

func (r *assessmentRubricRepository) Detach(ctx context.Context, assessmentID uuid.UUID) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM assessment_rubrics WHERE assessment_id = $1`, assessmentID)
	return helper.MapErr(err)
}

// GetRubric returns the attached rubric, or helper.ErrNotFound when the
// assessment has none. Callers treat ErrNotFound as "review without a rubric".
func (r *assessmentRubricRepository) GetRubric(ctx context.Context, assessmentID uuid.UUID) (*model.Rubric, error) {
	const q = `
		SELECT r.id, r.name, r.content, r.reusable, r.created_at, r.updated_at
		  FROM assessment_rubrics ar
		  JOIN rubrics r ON r.id = ar.rubric_id
		 WHERE ar.assessment_id = $1`
	var rb model.Rubric
	err := r.db.Querier(ctx).QueryRow(ctx, q, assessmentID).
		Scan(&rb.ID, &rb.Name, &rb.Content, &rb.Reusable, &rb.CreatedAt, &rb.UpdatedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &rb, nil
}
