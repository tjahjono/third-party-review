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
	"third-party-review/internal/helper"
	"third-party-review/internal/model"
	"third-party-review/internal/service"

	"github.com/google/uuid"
)

// OpenAICompat talks to any endpoint that speaks the OpenAI chat-completions
// shape. That covers a self-hosted Open WebUI in front of Qwen (base URL
// ending in /api), OpenAI itself (/v1), vLLM, Ollama's compat layer, LiteLLM
// and most internal gateways - which is why this, rather than a vendor SDK, is
// the default client.
//
// Structured output is requested but never relied on: what a proxied backend
// does with response_format varies, so the response is always mined for JSON
// (see ExtractJSON) and retried with a corrective message if that fails.
type OpenAICompat struct {
	cfg    config.AI
	http   *http.Client
	log    *slog.Logger
	name   string
	hasKey bool
}

// NewOpenAICompat constructs the client.
func NewOpenAICompat(cfg config.AI, log *slog.Logger) *OpenAICompat {
	return &OpenAICompat{
		cfg:    cfg,
		http:   &http.Client{Timeout: cfg.Timeout},
		log:    log,
		name:   "openaicompat",
		hasKey: strings.TrimSpace(cfg.APIKey) != "",
	}
}

// Name identifies the provider on persisted results.
func (c *OpenAICompat) Name() string { return c.name }

func (c *OpenAICompat) endpoint() string {
	base := strings.TrimRight(c.cfg.BaseURL, "/")
	path := c.cfg.ChatPath
	if path == "" {
		path = "/chat/completions"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return base + path
}

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaRequest struct {
	Model          string          `json:"model"`
	Messages       []oaMessage     `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stream         bool            `json:"stream"`
	ResponseFormat *oaResponseForm `json:"response_format,omitempty"`
}

type oaResponseForm struct {
	Type string `json:"type"`
}

type oaResponse struct {
	Choices []struct {
		Message      oaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// complete sends one chat completion and returns the assistant text. wantJSON
// asks the backend for JSON mode where it supports it.
func (c *OpenAICompat) complete(ctx context.Context, system, user string, wantJSON bool) (string, error) {
	reqBody := oaRequest{
		Model:       c.cfg.Model,
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
		Stream:      false,
		Messages: []oaMessage{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
	}
	if wantJSON {
		reqBody.ResponseFormat = &oaResponseForm{Type: "json_object"}
	}

	var lastErr error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			// Linear backoff is enough for a single team's request volume.
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		text, err := c.do(ctx, reqBody)
		if err == nil {
			return text, nil
		}
		lastErr = err
		if !retryable(err) {
			return "", err
		}
		// A backend that rejects response_format outright should still be
		// usable; drop it and let ExtractJSON do the work.
		if reqBody.ResponseFormat != nil && isBadRequest(err) {
			c.log.Warn("provider rejected response_format, retrying without JSON mode", "error", err)
			reqBody.ResponseFormat = nil
		}
		c.log.Warn("AI request failed, retrying", "attempt", attempt+1, "error", err)
	}
	return "", fmt.Errorf("ai: request failed after %d attempts: %w", c.cfg.MaxRetries+1, lastErr)
}

func (c *OpenAICompat) do(ctx context.Context, body oaRequest) (string, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("ai: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("ai: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.hasKey {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

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

	var parsed oaResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("ai: decode response (is %s an OpenAI-compatible endpoint?): %w", c.endpoint(), err)
	}
	if parsed.Error != nil && parsed.Error.Message != "" {
		return "", fmt.Errorf("ai: provider error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("ai: provider returned no choices")
	}
	if fr := parsed.Choices[0].FinishReason; fr == "length" {
		c.log.Warn("model output was truncated by the token limit; raise AI_MAX_TOKENS or lower AI_BATCH_SIZE",
			"max_tokens", c.cfg.MaxTokens)
	}
	return parsed.Choices[0].Message.Content, nil
}

// ReviewAnswer evaluates a single answer.
func (c *OpenAICompat) ReviewAnswer(ctx context.Context, req dto.ReviewRequest) (model.ReviewResult, error) {
	text, err := c.complete(ctx, systemPrompt, BuildSingle(req), true)
	if err != nil {
		return model.ReviewResult{}, err
	}
	return parseSingle(text, req.Question.QuestionID, c.name, c.cfg.Model)
}

// ReviewBatch evaluates several answers in one call.
func (c *OpenAICompat) ReviewBatch(ctx context.Context, req dto.BatchReviewRequest) (dto.BatchReviewResponse, error) {
	user := BuildBatch(req)
	text, err := c.complete(ctx, systemPrompt, user, true)
	if err != nil {
		return dto.BatchReviewResponse{}, err
	}

	out, err := parseBatch(text, c.name, c.cfg.Model)
	if err == nil {
		return out, nil
	}
	// One corrective round trip: models that wrapped the payload in prose or
	// lost the outer object usually get it right when told exactly what broke.
	c.log.Warn("model returned unparseable JSON, asking once more", "error", err)
	retry := user + "\n\nYour previous reply could not be parsed as JSON (" + err.Error() +
		"). Reply again with the JSON object only. No explanation, no markdown fence, no text before or after it."
	text2, err2 := c.complete(ctx, systemPrompt, retry, true)
	if err2 != nil {
		return dto.BatchReviewResponse{}, err
	}
	return parseBatch(text2, c.name, c.cfg.Model)
}

// Summarize writes the assessment-level narrative.
func (c *OpenAICompat) Summarize(ctx context.Context, req dto.SummaryRequest) (string, error) {
	text, err := c.complete(ctx, summarySystemPrompt, BuildSummary(req), false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stripFences(text)), nil
}

var _ service.AIReviewer = (*OpenAICompat)(nil)

const summarySystemPrompt = `You are an experienced third-party security assessor writing the executive summary of a completed vendor assessment for an internal risk file. You write plainly and specifically, never inflate or soften findings, and never introduce facts you were not given.`

// parseSingle decodes a single-question response.
func parseSingle(text string, questionID uuid.UUID, provider, modelName string) (model.ReviewResult, error) {
	var raw rawResult
	if err := helper.UnmarshalLoose(text, &raw); err != nil {
		// Some models answer a single-item prompt with the batch shape anyway.
		var batch rawBatch
		if err2 := helper.UnmarshalLoose(text, &batch); err2 == nil && len(batch.Results) > 0 {
			raw = batch.Results[0]
		} else {
			return model.ReviewResult{}, fmt.Errorf("ai: %w", err)
		}
	}
	res := raw.toResult(provider, modelName)
	// Trust our own id over whatever the model echoed back.
	res.QuestionID = questionID
	res.Raw = text
	return res, nil
}

// parseBatch decodes a multi-question response, tolerating both the documented
// {"results":[...]} shape and a bare array.
func parseBatch(text, provider, modelName string) (dto.BatchReviewResponse, error) {
	out := dto.BatchReviewResponse{Results: map[uuid.UUID]model.ReviewResult{}, Raw: text}

	var batch rawBatch
	if err := helper.UnmarshalLoose(text, &batch); err == nil && len(batch.Results) > 0 {
		for _, r := range batch.Results {
			if r.QuestionID == uuid.Nil {
				continue
			}
			res := r.toResult(provider, modelName)
			res.Raw = ""
			out.Results[r.QuestionID] = res
		}
		out.Notes = batch.Notes
		return out, nil
	}

	var arr []rawResult
	if err := helper.UnmarshalLoose(text, &arr); err == nil && len(arr) > 0 {
		for _, r := range arr {
			if r.QuestionID == uuid.Nil {
				continue
			}
			res := r.toResult(provider, modelName)
			res.Raw = ""
			out.Results[r.QuestionID] = res
		}
		return out, nil
	}

	return dto.BatchReviewResponse{}, fmt.Errorf("ai: could not read any results from the model response")
}

// stripFences removes a markdown fence wrapping prose output.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if inner, ok := helper.FencedBlock(s); ok {
		return inner
	}
	return s
}
