package repository

import (
	"context"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type AssessmentDomainRepository interface {
	List(ctx context.Context, includeInactive bool) ([]*model.AssessmentDomain, error)
	GetByID(ctx context.Context, id uuid.UUID) (*model.AssessmentDomain, error)
	GetBySlug(ctx context.Context, slug string) (*model.AssessmentDomain, error)
	Create(ctx context.Context, d *model.AssessmentDomain) error
	Update(ctx context.Context, d *model.AssessmentDomain) error
}

type assessmentDomainRepository struct {
	db helper.ConnProvider
}

func NewAssessmentDomainRepository(db helper.ConnProvider) AssessmentDomainRepository {
	return &assessmentDomainRepository{db: db}
}

const domainColumns = `id, name, slug, sort_order, scrutiny_note, active, created_at`

func (r *assessmentDomainRepository) List(ctx context.Context, includeInactive bool) ([]*model.AssessmentDomain, error) {
	const q = `
		SELECT ` + domainColumns + `
		  FROM assessment_domains
		 WHERE ($1 OR active)
		 ORDER BY sort_order, name`
	rows, err := r.db.Querier(ctx).Query(ctx, q, includeInactive)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.AssessmentDomain
	for rows.Next() {
		var d model.AssessmentDomain
		if err := rows.Scan(&d.ID, &d.Name, &d.Slug, &d.SortOrder, &d.ScrutinyNote, &d.Active, &d.CreatedAt); err != nil {
			return nil, helper.MapErr(err)
		}
		out = append(out, &d)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *assessmentDomainRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.AssessmentDomain, error) {
	return r.get(ctx, `SELECT `+domainColumns+` FROM assessment_domains WHERE id = $1`, id)
}

func (r *assessmentDomainRepository) GetBySlug(ctx context.Context, slug string) (*model.AssessmentDomain, error) {
	return r.get(ctx, `SELECT `+domainColumns+` FROM assessment_domains WHERE slug = $1`, slug)
}

func (r *assessmentDomainRepository) get(ctx context.Context, q string, arg any) (*model.AssessmentDomain, error) {
	var d model.AssessmentDomain
	err := r.db.Querier(ctx).QueryRow(ctx, q, arg).Scan(
		&d.ID, &d.Name, &d.Slug, &d.SortOrder, &d.ScrutinyNote, &d.Active, &d.CreatedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &d, nil
}

func (r *assessmentDomainRepository) Create(ctx context.Context, d *model.AssessmentDomain) error {
	const q = `
		INSERT INTO assessment_domains (name, slug, sort_order, scrutiny_note, active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, d.Name, d.Slug, d.SortOrder, d.ScrutinyNote, d.Active).
		Scan(&d.ID, &d.CreatedAt)
	return helper.MapErr(err)
}

func (r *assessmentDomainRepository) Update(ctx context.Context, d *model.AssessmentDomain) error {
	const q = `
		UPDATE assessment_domains
		   SET name = $2, slug = $3, sort_order = $4, scrutiny_note = $5, active = $6
		 WHERE id = $1`
	tag, err := r.db.Querier(ctx).Exec(ctx, q, d.ID, d.Name, d.Slug, d.SortOrder, d.ScrutinyNote, d.Active)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}
