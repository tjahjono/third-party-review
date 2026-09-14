package aiclient

import (
	"fmt"
	"log/slog"

	"third-party-review/internal/config"
	"third-party-review/internal/domain"
)

// New constructs the AIReviewer named by configuration. This is the only place
// that knows which providers exist; everything above it works through the
// domain.AIReviewer interface.
func New(cfg config.AI, log *slog.Logger) (domain.AIReviewer, error) {
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
