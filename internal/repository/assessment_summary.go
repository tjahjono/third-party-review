package repository

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"third-party-review/internal/helper"
	"third-party-review/internal/model"
)

type AssessmentSummaryRepository interface {
	Upsert(ctx context.Context, s *model.AssessmentSummary) error
	GetByAssessment(ctx context.Context, assessmentID uuid.UUID) (*model.AssessmentSummary, error)
}

type assessmentSummaryRepository struct {
	db helper.ConnProvider
}

func NewAssessmentSummaryRepository(db helper.ConnProvider) AssessmentSummaryRepository {
	return &assessmentSummaryRepository{db: db}
}

func (r *assessmentSummaryRepository) Upsert(ctx context.Context, s *model.AssessmentSummary) error {
	scores, err := json.Marshal(s.DomainScores)
	if err != nil {
		return fmt.Errorf("repository: encode domain_scores: %w", err)
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
	err = r.db.Querier(ctx).QueryRow(ctx, stmt,
		s.AssessmentID, s.RunID, s.QuestionCount, s.ScoredCount, s.OverallScore,
		s.MeanScore, int(s.WorstScore), s.FlaggedCount, s.IncompleteCount,
		s.PendingFinalization, s.FinalizedCount, scores, s.Narrative,
	).Scan(&s.GeneratedAt)
	return helper.MapErr(err)
}

func (r *assessmentSummaryRepository) GetByAssessment(ctx context.Context, assessmentID uuid.UUID) (*model.AssessmentSummary, error) {
	const stmt = `
		SELECT assessment_id, run_id, question_count, scored_count, overall_score,
		       mean_score, worst_score, flagged_count, incomplete_count,
		       pending_finalization, finalized_count, domain_scores, narrative, generated_at
		  FROM assessment_summaries WHERE assessment_id = $1`
	var (
		s      model.AssessmentSummary
		scores []byte
		worst  int
	)
	err := r.db.Querier(ctx).QueryRow(ctx, stmt, assessmentID).Scan(
		&s.AssessmentID, &s.RunID, &s.QuestionCount, &s.ScoredCount, &s.OverallScore,
		&s.MeanScore, &worst, &s.FlaggedCount, &s.IncompleteCount,
		&s.PendingFinalization, &s.FinalizedCount, &scores, &s.Narrative, &s.GeneratedAt)
	if err != nil {
		return nil, helper.MapErr(err)
	}
	s.WorstScore = model.RiskScore(worst)
	if len(scores) > 0 {
		if err := json.Unmarshal(scores, &s.DomainScores); err != nil {
			return nil, fmt.Errorf("repository: decode domain_scores for assessment %s: %w", assessmentID, err)
		}
	}
	helper.SortDomainScores(s.DomainScores)
	return &s, nil
}
