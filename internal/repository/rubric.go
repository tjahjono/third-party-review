package repository

import (
	"context"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type RubricRepository interface {
	Create(ctx context.Context, r *model.Rubric) error
	Update(ctx context.Context, r *model.Rubric) error
	GetByID(ctx context.Context, id uuid.UUID) (*model.Rubric, error)
	ListReusable(ctx context.Context) ([]*model.Rubric, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type rubricRepository struct {
	db helper.ConnProvider
}

func NewRubricRepository(db helper.ConnProvider) RubricRepository {
	return &rubricRepository{db: db}
}

const rubricColumns = `id, name, content, reusable, created_at, updated_at`

func (r *rubricRepository) Create(ctx context.Context, rb *model.Rubric) error {
	const q = `INSERT INTO rubrics (name, content, reusable) VALUES ($1,$2,$3)
	           RETURNING id, created_at, updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, rb.Name, rb.Content, rb.Reusable).
		Scan(&rb.ID, &rb.CreatedAt, &rb.UpdatedAt)
	return helper.MapErr(err)
}

func (r *rubricRepository) Update(ctx context.Context, rb *model.Rubric) error {
	const q = `UPDATE rubrics SET name=$2, content=$3, reusable=$4 WHERE id=$1 RETURNING updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, rb.ID, rb.Name, rb.Content, rb.Reusable).Scan(&rb.UpdatedAt)
	return helper.MapErr(err)
}

func (r *rubricRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.Rubric, error) {
	const q = `SELECT ` + rubricColumns + ` FROM rubrics WHERE id = $1`
	var rb model.Rubric
	err := r.db.Querier(ctx).QueryRow(ctx, q, id).Scan(&rb.ID, &rb.Name, &rb.Content, &rb.Reusable, &rb.CreatedAt, &rb.UpdatedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &rb, nil
}

func (r *rubricRepository) ListReusable(ctx context.Context) ([]*model.Rubric, error) {
	const q = `SELECT ` + rubricColumns + ` FROM rubrics WHERE reusable ORDER BY name`
	rows, err := r.db.Querier(ctx).Query(ctx, q)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.Rubric
	for rows.Next() {
		var rb model.Rubric
		if err := rows.Scan(&rb.ID, &rb.Name, &rb.Content, &rb.Reusable, &rb.CreatedAt, &rb.UpdatedAt); err != nil {
			return nil, helper.MapErr(err)
		}
		out = append(out, &rb)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *rubricRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM rubrics WHERE id = $1`, id)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}
