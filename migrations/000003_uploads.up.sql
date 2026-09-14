-- The raw uploaded questionnaire is kept so the mapping step can be revisited
-- (or a different worksheet chosen) without asking the user to upload again,
-- and so an ingestion decision stays auditable against the original file.
--
-- Stored in Postgres rather than on a volume deliberately: it keeps the
-- deployment to app + database with no shared filesystem, and a questionnaire
-- is a few hundred kilobytes. Content is dropped once the assessment closes.
CREATE TABLE assessment_uploads (
    assessment_id BIGINT PRIMARY KEY REFERENCES assessments(id) ON DELETE CASCADE,
    filename      TEXT        NOT NULL,
    content_type  TEXT        NOT NULL DEFAULT '',
    content       BYTEA       NOT NULL,
    byte_size     BIGINT      NOT NULL,
    sha256        TEXT        NOT NULL DEFAULT '',
    sheet_name    TEXT        NOT NULL DEFAULT '',
    uploaded_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
