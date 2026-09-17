-- Lets an account be deactivated (login blocked) without deleting it, so
-- rows it authored - finalized feedback, past logins - stay intact and
-- attributable. Defaults every existing account to active so this migration
-- changes nobody's ability to sign in.
ALTER TABLE users ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE;
