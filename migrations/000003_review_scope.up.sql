-- Selective AI review: a run can cover every question ("all", the existing
-- behaviour), only questions never yet reviewed ("unreviewed"), or a
-- user-chosen subset ("selected"). The chosen subset is stored in its own
-- join table rather than as a JSON/array column on review_jobs, so it can be
-- indexed and queried like any other relationship.

ALTER TABLE review_jobs
    ADD COLUMN scope TEXT NOT NULL DEFAULT 'all'
        CHECK (scope IN ('all', 'unreviewed', 'selected'));

CREATE TABLE review_job_questions (
    job_id      UUID NOT NULL REFERENCES review_jobs(id) ON DELETE CASCADE,
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    PRIMARY KEY (job_id, question_id)
);

CREATE INDEX idx_review_job_questions_question ON review_job_questions(question_id);
