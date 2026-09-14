package postgres

import (
	"context"

	"third-party-review/internal/domain"
)

// DomainRepo is the PostgreSQL implementation of domain.DomainRepository. The
// 8 TPSA domains are seeded by migration 000002 but the table is writable, so
// new domains can be added without a schema change.
type DomainRepo struct{ db *DB }

const domainCols = `id, name, slug, sort_order, scrutiny_note, active, created_at`

func (r *DomainRepo) List(ctx context.Context, includeInactive bool) ([]*domain.AssessmentDomain, error) {
	const q = `
		SELECT ` + domainCols + `
		  FROM assessment_domains
		 WHERE ($1 OR active)
		 ORDER BY sort_order, name`
	rows, err := r.db.q(ctx).Query(ctx, q, includeInactive)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []*domain.AssessmentDomain
	for rows.Next() {
		var d domain.AssessmentDomain
		if err := rows.Scan(&d.ID, &d.Name, &d.Slug, &d.SortOrder, &d.ScrutinyNote, &d.Active, &d.CreatedAt); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, &d)
	}
	return out, mapErr(rows.Err())
}

func (r *DomainRepo) GetByID(ctx context.Context, id int64) (*domain.AssessmentDomain, error) {
	return r.get(ctx, `SELECT `+domainCols+` FROM assessment_domains WHERE id = $1`, id)
}

func (r *DomainRepo) GetBySlug(ctx context.Context, slug string) (*domain.AssessmentDomain, error) {
	return r.get(ctx, `SELECT `+domainCols+` FROM assessment_domains WHERE slug = $1`, slug)
}

func (r *DomainRepo) get(ctx context.Context, q string, arg any) (*domain.AssessmentDomain, error) {
	var d domain.AssessmentDomain
	err := r.db.q(ctx).QueryRow(ctx, q, arg).Scan(
		&d.ID, &d.Name, &d.Slug, &d.SortOrder, &d.ScrutinyNote, &d.Active, &d.CreatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}

func (r *DomainRepo) Create(ctx context.Context, d *domain.AssessmentDomain) error {
	const q = `
		INSERT INTO assessment_domains (name, slug, sort_order, scrutiny_note, active)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, created_at`
	err := r.db.q(ctx).QueryRow(ctx, q, d.Name, d.Slug, d.SortOrder, d.ScrutinyNote, d.Active).
		Scan(&d.ID, &d.CreatedAt)
	return mapErr(err)
}

func (r *DomainRepo) Update(ctx context.Context, d *domain.AssessmentDomain) error {
	const q = `
		UPDATE assessment_domains
		   SET name = $2, slug = $3, sort_order = $4, scrutiny_note = $5, active = $6
		 WHERE id = $1`
	tag, err := r.db.q(ctx).Exec(ctx, q, d.ID, d.Name, d.Slug, d.SortOrder, d.ScrutinyNote, d.Active)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
