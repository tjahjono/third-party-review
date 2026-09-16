package aiclient

import (
	"context"
	"fmt"
	"log/slog"

	"third-party-review/internal/config"
	"third-party-review/internal/dto"
	"third-party-review/internal/model"
)

type AIReviewer interface {
	ReviewBatch(ctx context.Context, req dto.BatchReviewRequest) (dto.BatchReviewResponse, error)
	ReviewAnswer(ctx context.Context, req dto.ReviewRequest) (model.ReviewResult, error)
	Summarize(ctx context.Context, req dto.SummaryRequest) (string, error)
	Name() string
}

func New(cfg config.AI, log *slog.Logger) (AIReviewer, error) {
	switch cfg.Provider {
	case config.ProviderOpenAICompat:
		log.Info("AI provider configured", "provider", cfg.Provider, "model", cfg.Model, "base_url", cfg.BaseURL)
		return NewOpenAICompat(cfg, log), nil
	case config.ProviderAnthropic:
		log.Info("AI provider configured", "provider", cfg.Provider, "model", cfg.Model, "base_url", cfg.BaseURL)
		return NewAnthropic(cfg, log), nil
	default:
		return nil, fmt.Errorf("aiclient: unknown provider %q", cfg.Provider)
	}
}
