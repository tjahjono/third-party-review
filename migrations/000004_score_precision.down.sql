ALTER TABLE review_results
    ALTER COLUMN confidence TYPE REAL;

ALTER TABLE assessment_summaries
    ALTER COLUMN overall_score TYPE REAL,
    ALTER COLUMN mean_score    TYPE REAL;
