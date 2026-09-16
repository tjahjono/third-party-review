package aiclient

import (
	"context"
	"fmt"
	"log/slog"

	"third-party-review/internal/config"
	"third-party-review/internal/dto"
	"third-party-review/internal/model"
)

// AIReviewer is the provider-agnostic contract for AI-assisted review. Every
// concrete client (Open WebUI / Qwen, OpenAI, Anthropic, mock) implements it,
// so the service layer never learns which provider is configured.
type AIReviewer interface {
	// ReviewBatch evaluates a group of answers together, which lets the model
	// spot contradictions between answers in the same assessment. Results are
	// returned keyed by QuestionID; a missing key means the model did not
	// return a usable result for that question and it should be retried
	// individually.
	ReviewBatch(ctx context.Context, req dto.BatchReviewRequest) (dto.BatchReviewResponse, error)

	// ReviewAnswer evaluates one answer on its own. Used as a follow-up for
	// questions the batch pass skipped or scored with low confidence.
	ReviewAnswer(ctx context.Context, req dto.ReviewRequest) (model.ReviewResult, error)

	// Summarize writes the assessment-level narrative from the per-question
	// findings. Aggregated numbers are computed in Go, not by the model.
	Summarize(ctx context.Context, req dto.SummaryRequest) (string, error)

	// Name identifies the provider for logging and for the Provider column on
	// persisted results.
	Name() string
}

// New constructs the AIReviewer named by configuration. This is the only place
// that knows which providers exist; everything above it works through the
// AIReviewer interface.
func New(cfg config.AI, log *slog.Logger) (AIReviewer, error) {
	switch cfg.Provider {
	case config.ProviderOpenAICompat:
		log.Info("AI provider configured", "provider", cfg.Provider, "model", cfg.Model, "base_url", cfg.BaseURL)
		return NewOpenAICompat(cfg, log), nil
	case config.ProviderAnthropic:
		log.Info("AI provider configured", "provider", cfg.Provider, "model", cfg.Model, "base_url", cfg.BaseURL)
		return NewAnthropic(cfg, log), nil
	case config.ProviderMock:
		log.Warn("AI provider is the deterministic mock; reviews will not be model-generated")
		return NewMock(), nil
	default:
		return nil, fmt.Errorf("aiclient: unknown provider %q", cfg.Provider)
	}
}
