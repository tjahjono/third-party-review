package aiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"third-party-review/internal/config"
	"third-party-review/internal/dto"
	"third-party-review/internal/model"
)

type anthropic struct {
	cfg  config.AI
	http *http.Client
	log  *slog.Logger
}

func NewAnthropic(cfg config.AI, log *slog.Logger) AIReviewer {
	return &anthropic{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
		log:  log,
	}
}

func (c *anthropic) ReviewAnswer(ctx context.Context, req dto.ReviewRequest) (model.ReviewResult, error) {
	text, err := c.complete(ctx, systemPrompt, BuildSingle(req))
	if err != nil {
		return model.ReviewResult{}, err
	}
	return parseSingle(text, req.Question.QuestionID, c.Name(), c.cfg.Model)
}

// ReviewBatch evaluates several answers in one call.
func (c *anthropic) ReviewBatch(ctx context.Context, req dto.BatchReviewRequest) (dto.BatchReviewResponse, error) {
	user := BuildBatch(req)
	text, err := c.complete(ctx, systemPrompt, user)
	if err != nil {
		return dto.BatchReviewResponse{}, err
	}
	out, err := parseBatch(text, c.Name(), c.cfg.Model)
	if err == nil {
		return out, nil
	}
	c.log.Warn("model returned unparseable JSON, asking once more", "error", err)
	retry := user + "\n\nYour previous reply could not be parsed as JSON (" + err.Error() +
		"). Reply again with the JSON object only. No explanation, no markdown fence, no text before or after it."
	text2, err2 := c.complete(ctx, systemPrompt, retry)
	if err2 != nil {
		return dto.BatchReviewResponse{}, err
	}
	return parseBatch(text2, c.Name(), c.cfg.Model)
}

// Summarize writes the assessment-level narrative.
func (c *anthropic) Summarize(ctx context.Context, req dto.SummaryRequest) (string, error) {
	text, err := c.complete(ctx, summarySystemPrompt, BuildSummary(req))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stripFences(text)), nil
}

// Name identifies the provider on persisted results.
func (c *anthropic) Name() string { return "anthropic" }

func (c *anthropic) endpoint() string {
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	if strings.HasSuffix(base, "/messages") {
		return base
	}
	return base + "/messages"
}

func (c *anthropic) complete(ctx context.Context, system, user string) (string, error) {
	maxTokens := c.cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	body := model.AnthropicRequest{
		Model:       c.cfg.Model,
		System:      system,
		MaxTokens:   maxTokens,
		Temperature: c.cfg.Temperature,
		Messages:    []model.AnthropicMessage{{Role: "user", Content: user}},
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		text, err := c.do(ctx, body)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !retryable(err) {
			return "", err
		}
		c.log.Warn("AI request failed, retrying", "attempt", attempt+1, "error", err)
	}
	return "", fmt.Errorf("ai: request failed after %d attempts: %w", c.cfg.MaxRetries+1, lastErr)
}

func (c *anthropic) do(ctx context.Context, body model.AnthropicRequest) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("ai: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", &transportError{err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", &transportError{err: err}
	}
	if resp.StatusCode != http.StatusOK {
		return "", &httpError{status: resp.StatusCode, body: string(raw), endpoint: c.endpoint()}
	}

	var parsed model.AnthropicResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("ai: decode response: %w", err)
	}
	if parsed.Error != nil {
		return "", fmt.Errorf("ai: provider error: %s", parsed.Error.Message)
	}
	if parsed.StopReason == "max_tokens" {
		c.log.Warn("model output was truncated by the token limit; raise AI_MAX_TOKENS or lower AI_BATCH_SIZE",
			"max_tokens", body.MaxTokens)
	}

	var sb strings.Builder
	for _, block := range parsed.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("ai: provider returned no text content")
	}
	return sb.String(), nil
}
