package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"third-party-review/internal/domain"
)

// SummaryRepo is the PostgreSQL implementation of domain.SummaryRepository.
type SummaryRepo struct{ db *DB }

func (r *SummaryRepo) Upsert(ctx context.Context, s *domain.AssessmentSummary) error {
	scores, err := json.Marshal(s.DomainScores)
	if err != nil {
		return fmt.Errorf("postgres: encode domain_scores: %w", err)
	}
	if s.DomainScores == nil {
		scores = []byte("[]")
	}
	const stmt = `
		INSERT INTO assessment_summaries (
			assessment_id, run_id, question_count, scored_count, overall_score,
			mean_score, worst_score, flagged_count, incomplete_count,
			pending_finalization, finalized_count, domain_scores, narrative, generated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13, now())
		ON CONFLICT (assessment_id) DO UPDATE SET
			run_id = EXCLUDED.run_id,
			question_count = EXCLUDED.question_count,
			scored_count = EXCLUDED.scored_count,
			overall_score = EXCLUDED.overall_score,
			mean_score = EXCLUDED.mean_score,
			worst_score = EXCLUDED.worst_score,
			flagged_count = EXCLUDED.flagged_count,
			incomplete_count = EXCLUDED.incomplete_count,
			pending_finalization = EXCLUDED.pending_finalization,
			finalized_count = EXCLUDED.finalized_count,
			domain_scores = EXCLUDED.domain_scores,
			narrative = EXCLUDED.narrative,
			generated_at = now()
		RETURNING generated_at`
	err = r.db.q(ctx).QueryRow(ctx, stmt,
		s.AssessmentID, s.RunID, s.QuestionCount, s.ScoredCount, s.OverallScore,
		s.MeanScore, int(s.WorstScore), s.FlaggedCount, s.IncompleteCount,
		s.PendingFinalization, s.FinalizedCount, scores, s.Narrative,
	).Scan(&s.GeneratedAt)
	return mapErr(err)
}

func (r *SummaryRepo) GetByAssessment(ctx context.Context, assessmentID int64) (*domain.AssessmentSummary, error) {
	const stmt = `
		SELECT assessment_id, run_id, question_count, scored_count, overall_score,
		       mean_score, worst_score, flagged_count, incomplete_count,
		       pending_finalization, finalized_count, domain_scores, narrative, generated_at
		  FROM assessment_summaries WHERE assessment_id = $1`
	var (
		s      domain.AssessmentSummary
		scores []byte
		worst  int
	)
	err := r.db.q(ctx).QueryRow(ctx, stmt, assessmentID).Scan(
		&s.AssessmentID, &s.RunID, &s.QuestionCount, &s.ScoredCount, &s.OverallScore,
		&s.MeanScore, &worst, &s.FlaggedCount, &s.IncompleteCount,
		&s.PendingFinalization, &s.FinalizedCount, &scores, &s.Narrative, &s.GeneratedAt)
	if err != nil {
		return nil, mapErr(err)
	}
	s.WorstScore = domain.RiskScore(worst)
	if len(scores) > 0 {
		if err := json.Unmarshal(scores, &s.DomainScores); err != nil {
			return nil, fmt.Errorf("postgres: decode domain_scores for assessment %d: %w", assessmentID, err)
		}
	}
	domain.SortDomainScores(s.DomainScores)
	return &s, nil
}

var _ domain.SummaryRepository = (*SummaryRepo)(nil)
