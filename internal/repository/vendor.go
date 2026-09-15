package repository

import (
	"context"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type VendorRepository interface {
	Create(ctx context.Context, v *model.Vendor) error
	Update(ctx context.Context, v *model.Vendor) error
	GetByID(ctx context.Context, id uuid.UUID) (*model.Vendor, error)
	List(ctx context.Context, search string, limit, offset int) ([]*model.Vendor, error)
	Count(ctx context.Context, search string) (int, error)
	Delete(ctx context.Context, id uuid.UUID) error
}

type vendorRepository struct {
	db helper.ConnProvider
}

func NewVendorRepository(db helper.ConnProvider) VendorRepository {
	return &vendorRepository{db: db}
}

const vendorColumns = `id, name, contact_name, contact_email, notes, created_at, updated_at`

func (r *vendorRepository) Create(ctx context.Context, v *model.Vendor) error {
	const q = `
		INSERT INTO vendors (name, contact_name, contact_email, notes)
		VALUES ($1, $2, $3, $4)
		RETURNING id, created_at, updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, v.Name, v.ContactName, v.ContactEmail, v.Notes).
		Scan(&v.ID, &v.CreatedAt, &v.UpdatedAt)
	return helper.MapErr(err)
}

func (r *vendorRepository) Update(ctx context.Context, v *model.Vendor) error {
	const q = `
		UPDATE vendors
		   SET name = $2, contact_name = $3, contact_email = $4, notes = $5
		 WHERE id = $1
		RETURNING updated_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, v.ID, v.Name, v.ContactName, v.ContactEmail, v.Notes).
		Scan(&v.UpdatedAt)
	return helper.MapErr(err)
}

func (r *vendorRepository) GetByID(ctx context.Context, id uuid.UUID) (*model.Vendor, error) {
	const q = `SELECT ` + vendorColumns + ` FROM vendors WHERE id = $1`
	var v model.Vendor
	err := r.db.Querier(ctx).QueryRow(ctx, q, id).Scan(
		&v.ID, &v.Name, &v.ContactName, &v.ContactEmail, &v.Notes, &v.CreatedAt, &v.UpdatedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &v, nil
}

func (r *vendorRepository) List(ctx context.Context, search string, limit, offset int) ([]*model.Vendor, error) {
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
	rows, err := r.db.Querier(ctx).Query(ctx, q, search, helper.LikeArg(search), limit, offset)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	defer rows.Close()

	var out []*model.Vendor
	for rows.Next() {
		var v model.Vendor
		if err := rows.Scan(&v.ID, &v.Name, &v.ContactName, &v.ContactEmail, &v.Notes,
			&v.CreatedAt, &v.UpdatedAt, &v.AssessmentCount); err != nil {
			return nil, helper.MapErr(err)
		}
		out = append(out, &v)
	}
	return out, helper.MapErr(rows.Err())
}

func (r *vendorRepository) Count(ctx context.Context, search string) (int, error) {
	const q = `
		SELECT COUNT(*) FROM vendors
		 WHERE ($1 = '' OR name ILIKE $2 ESCAPE '\' OR contact_email ILIKE $2 ESCAPE '\')`
	var n int
	err := r.db.Querier(ctx).QueryRow(ctx, q, search, helper.LikeArg(search)).Scan(&n)
	return n, helper.MapErr(err)
}

func (r *vendorRepository) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM vendors WHERE id = $1`, id)
	if err != nil {
		return helper.MapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return helper.ErrNotFound
	}
	return nil
}
