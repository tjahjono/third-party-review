package postgres

import (
	"context"

	"third-party-review/internal/domain"
)

// VendorRepo is the PostgreSQL implementation of domain.VendorRepository.
type VendorRepo struct{ db *DB }

const vendorCols = `id, name, contact_name, contact_email, notes, created_at, updated_at`

func (r *VendorRepo) Create(ctx context.Context, v *domain.Vendor) error {
	if err := v.Validate(); err != nil {
		return err
	}
	const q = `
		INSERT INTO vendors (name, contact_name, contact_email, notes)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, v.Name, v.ContactName, v.ContactEmail, v.Notes).
		Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
	return mapErr(err)
}

func (r *VendorRepo) Update(ctx context.Context, v *domain.Vendor) error {
	if err := v.Validate(); err != nil {
		return err
	}
	const q = `
		UPDATE vendors
		   SET name = $2, contact_name = $3, contact_email = $4, notes = $5
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.q(ctx).QueryRow(ctx, q, v.ID, v.Name, v.ContactName, v.ContactEmail, v.Notes).
		Scan(&v.UpdatedAt)
	return mapErr(err)
}

func (r *VendorRepo) GetByID(ctx context.Context, id int64) (*domain.Vendor, error) {
	const q = `SELECT ` + vendorCols + ` FROM vendors WHERE id = $1`
	var v domain.Vendor
	err := r.db.q(ctx).QueryRow(ctx, q, id).Scan(
		&v.ID, &v.Name, &v.ContactName, &v.ContactEmail, &v.Notes, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v, nil
}

func (r *VendorRepo) List(ctx context.Context, search string, limit, offset int) ([]*domain.Vendor, error) {
	if limit <= 0 {
		limit = 50
	}
	const q = `
		SELECT v.id, v.name, v.contact_name, v.contact_email, v.notes,
		       v.created_at, v.updated_at,
		       COUNT(a.id) AS assessment_count
		  FROM vendors v
		  LEFT JOIN assessments a ON a.vendor_id = v.id
		 WHERE ($1 = '' OR v.name ILIKE $2 ESCAPE '\' OR v.contact_email ILIKE $2 ESCAPE '\')
		 GROUP BY v.id
		 ORDER BY v.name
		 LIMIT $3 OFFSET $4`
	rows, err := r.db.q(ctx).Query(ctx, q, search, likeArg(search), limit, offset)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()

	var out []*domain.Vendor
	for rows.Next() {
		var v domain.Vendor
		if err := rows.Scan(&v.ID, &v.Name, &v.ContactName, &v.ContactEmail, &v.Notes,
			&v.CreatedAt, &v.UpdatedAt, &v.AssessmentCount); err != nil {
			return nil, mapErr(err)
		}
		out = append(out, &v)
	}
	return out, mapErr(rows.Err())
}

func (r *VendorRepo) Count(ctx context.Context, search string) (int, error) {
	const q = `
		SELECT COUNT(*) FROM vendors
		 WHERE ($1 = '' OR name ILIKE $2 ESCAPE '\' OR contact_email ILIKE $2 ESCAPE '\')`
	var n int
	err := r.db.q(ctx).QueryRow(ctx, q, search, likeArg(search)).Scan(&n)
	return n, mapErr(err)
}

func (r *VendorRepo) Delete(ctx context.Context, id int64) error {
	tag, err := r.db.q(ctx).Exec(ctx, `DELETE FROM vendors WHERE id = $1`, id)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}
