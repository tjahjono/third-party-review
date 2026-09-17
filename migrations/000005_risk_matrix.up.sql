-- The severity risk matrix - which raw 1-5 AI risk scores count as Low,
-- Medium, High or Critical - becomes an app-wide setting instead of a
-- hardcoded mapping. This is a single-tenant tool (see project non-goals), so
-- there is exactly one matrix for everyone: the table is a singleton, forced
-- to a single row by the boolean primary key trick (id can only ever be
-- TRUE), the same way a one-row "current configuration" table is usually
-- modelled in Postgres without a separate uniqueness constraint to maintain.
--
-- medium_min/high_min/critical_min are the lowest score that counts as that
-- band; everything below medium_min is Low. They default to the matrix this
-- app originally shipped with (1-2 Low, 3 Medium, 4 High, 5 Critical) so
-- nothing changes in effect until someone edits the setting.

CREATE TABLE app_settings (
    id                BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    risk_medium_min   SMALLINT    NOT NULL DEFAULT 3 CHECK (risk_medium_min BETWEEN 1 AND 5),
    risk_high_min     SMALLINT    NOT NULL DEFAULT 4 CHECK (risk_high_min BETWEEN 1 AND 5),
    risk_critical_min SMALLINT    NOT NULL DEFAULT 5 CHECK (risk_critical_min BETWEEN 1 AND 5),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by        UUID REFERENCES users(id) ON DELETE SET NULL,
    CHECK (risk_medium_min <= risk_high_min AND risk_high_min <= risk_critical_min)
);

INSERT INTO app_settings (id) VALUES (TRUE);
