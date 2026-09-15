package repository

import (
	"context"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type UploadRepository interface {
	Save(ctx context.Context, u *model.Upload) error
	Get(ctx context.Context, assessmentID uuid.UUID) (*model.Upload, error)
	SetSheet(ctx context.Context, assessmentID uuid.UUID, sheet string) error
	Discard(ctx context.Context, assessmentID uuid.UUID) error
}

type uploadRepository struct {
	db helper.ConnProvider
}

func NewUploadRepository(db helper.ConnProvider) UploadRepository {
	return &uploadRepository{db: db}
}

func (r *uploadRepository) Save(ctx context.Context, u *model.Upload) error {
	const q = `
		INSERT INTO assessment_uploads (assessment_id, filename, content_type, content, byte_size, sha256, sheet_name)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (assessment_id) DO UPDATE SET
			filename = EXCLUDED.filename,
			content_type = EXCLUDED.content_type,
			content = EXCLUDED.content,
			byte_size = EXCLUDED.byte_size,
			sha256 = EXCLUDED.sha256,
			sheet_name = EXCLUDED.sheet_name,
			uploaded_at = now()
		RETURNING uploaded_at`
	err := r.db.Querier(ctx).QueryRow(ctx, q, u.AssessmentID, u.Filename, u.ContentType,
		u.Content, u.ByteSize, u.SHA256, u.SheetName).Scan(&u.UploadedAt)
	return helper.MapErr(err)
}

func (r *uploadRepository) Get(ctx context.Context, assessmentID uuid.UUID) (*model.Upload, error) {
	const q = `
		SELECT assessment_id, filename, content_type, content, byte_size, sha256, sheet_name, uploaded_at
		  FROM assessment_uploads WHERE assessment_id = $1`
	var u model.Upload
	err := r.db.Querier(ctx).QueryRow(ctx, q, assessmentID).Scan(
		&u.AssessmentID, &u.Filename, &u.ContentType, &u.Content,
		&u.ByteSize, &u.SHA256, &u.SheetName, &u.UploadedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	return &u, nil
}

func (r *uploadRepository) SetSheet(ctx context.Context, assessmentID uuid.UUID, sheet string) error {
	_, err := r.db.Querier(ctx).Exec(ctx,
		`UPDATE assessment_uploads SET sheet_name = $2 WHERE assessment_id = $1`, assessmentID, sheet)
	return helper.MapErr(err)
}

func (r *uploadRepository) Discard(ctx context.Context, assessmentID uuid.UUID) error {
	_, err := r.db.Querier(ctx).Exec(ctx, `DELETE FROM assessment_uploads WHERE assessment_id = $1`, assessmentID)
	return helper.MapErr(err)
}
