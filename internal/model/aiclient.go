package model

type AnthropicRequest struct {
	Model       string             `json:"model"`
	System      string             `json:"system,omitempty"`
	Messages    []AnthropicMessage `json:"messages"`
	MaxTokens   int                `json:"max_tokens"`
	Temperature float64            `json:"temperature"`
}

type AnthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type AnthropicResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type OaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OaRequest struct {
	Model          string          `json:"model"`
	Messages       []OaMessage     `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Stream         bool            `json:"stream"`
	ResponseFormat *OaResponseForm `json:"response_format,omitempty"`
}

type OaResponseForm struct {
	Type string `json:"type"`
}

type OaResponse struct {
	Choices []struct {
		Message      OaMessage `json:"message"`
		FinishReason string    `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
