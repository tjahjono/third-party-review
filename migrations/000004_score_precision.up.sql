-- Scores are computed as float64 in Go and rounded to two decimals. Storing
-- them as REAL (float32) means the value read back is not bit-identical to the
-- one written, so a recomputation that changed nothing still looked like a
-- change. DOUBLE PRECISION round-trips exactly.
ALTER TABLE assessment_summaries
    ALTER COLUMN overall_score TYPE DOUBLE PRECISION,
    ALTER COLUMN mean_score    TYPE DOUBLE PRECISION;

ALTER TABLE review_results
    ALTER COLUMN confidence TYPE DOUBLE PRECISION;
