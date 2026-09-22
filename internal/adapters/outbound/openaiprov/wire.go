package openaiprov

// chatRequest is the OpenAI-compatible chat completions request body.
type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	LogProbs       bool            `json:"logprobs,omitempty"`
	TopLogProbs    int             `json:"top_logprobs,omitempty"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
	Temperature    *float64        `json:"temperature,omitempty"`
	Seed           *int            `json:"seed,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string       `json:"type"`
	JSONSchema *namedSchema `json:"json_schema,omitempty"`
}

type namedSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

// chatResponse is the OpenAI-compatible chat completions response body.
type chatResponse struct {
	Model   string `json:"model"`
	Usage   usage  `json:"usage"`
	Choices []struct {
		Message  chatMessage `json:"message"`
		LogProbs *struct {
			Content []struct {
				Token       string  `json:"token"`
				LogProb     float64 `json:"logprob"`
				TopLogProbs []struct {
					Token   string  `json:"token"`
					LogProb float64 `json:"logprob"`
				} `json:"top_logprobs"`
			} `json:"content"`
		} `json:"logprobs"`
	} `json:"choices"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}
