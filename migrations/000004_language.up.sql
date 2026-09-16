-- AI response language preference: a per-user account setting, snapshotted
-- onto each review job at enqueue time so a run always uses the language its
-- owner asked for, and so the worker (which always re-reads the job fresh
-- from the database rather than trusting anything in memory) has it without
-- looking the user back up.

ALTER TABLE users
    ADD COLUMN language TEXT NOT NULL DEFAULT 'en'
        CHECK (language IN ('en', 'id'));

ALTER TABLE review_jobs
    ADD COLUMN language TEXT NOT NULL DEFAULT 'en'
        CHECK (language IN ('en', 'id'));
