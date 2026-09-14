DROP TRIGGER IF EXISTS rubrics_set_updated_at     ON rubrics;
DROP TRIGGER IF EXISTS questions_set_updated_at   ON questions;
DROP TRIGGER IF EXISTS assessments_set_updated_at ON assessments;
DROP TRIGGER IF EXISTS vendors_set_updated_at     ON vendors;
DROP TRIGGER IF EXISTS users_set_updated_at       ON users;
DROP FUNCTION IF EXISTS set_updated_at();

ALTER TABLE IF EXISTS assessments DROP CONSTRAINT IF EXISTS assessments_current_run_fk;

DROP TABLE IF EXISTS assessment_summaries;
DROP TABLE IF EXISTS review_results;
DROP TABLE IF EXISTS review_jobs;
DROP TABLE IF EXISTS assessment_rubrics;
DROP TABLE IF EXISTS rubrics;
DROP TABLE IF EXISTS questions;
DROP TABLE IF EXISTS assessments;
DROP TABLE IF EXISTS vendors;
DROP TABLE IF EXISTS assessment_domains;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS users;
