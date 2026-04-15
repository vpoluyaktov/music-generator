package openai

import (
	_ "embed"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	goopenai "github.com/sashabaranov/go-openai"
)

// ErrEmptyResponse is returned when OpenAI returns an empty or whitespace-only response.
var ErrEmptyResponse = errors.New("OpenAI returned empty response")

// ErrMalformedABC is returned when the cleaned response does not contain a valid ABC tune header.
var ErrMalformedABC = errors.New("OpenAI response does not contain valid ABC notation")

// openFenceRE matches an opening markdown code fence (``` optionally followed by a language tag).
var openFenceRE = regexp.MustCompile("^```[^\\s]{0,20}\\s*\\n")

// closeFenceRE matches a closing markdown code fence at the end of the string.
var closeFenceRE = regexp.MustCompile("\\n?```\\s*$")

//go:embed system_prompt.txt
var systemPrompt string

// Generator is the interface for generating ABC notation melodies.
type Generator interface {
	GenerateMelody(ctx context.Context, prompt string) (string, error)
}

// OpenAIGenerator implements Generator using the OpenAI API.
type OpenAIGenerator struct {
	client      *goopenai.Client
	model       string
	maxTokens   int
	temperature float64
}

// NewOpenAIGenerator creates a new OpenAIGenerator.
func NewOpenAIGenerator(apiKey, model string, maxTokens int, temperature float64) *OpenAIGenerator {
	return &OpenAIGenerator{
		client:      goopenai.NewClient(apiKey),
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
	}
}

// NewOpenAIGeneratorWithBaseURL creates a new OpenAIGenerator with a custom base URL.
// This is intended for testing purposes (e.g. pointing at a mock HTTP server).
func NewOpenAIGeneratorWithBaseURL(apiKey, model string, maxTokens int, temperature float64, baseURL string) *OpenAIGenerator {
	cfg := goopenai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL + "/v1"
	return &OpenAIGenerator{
		client:      goopenai.NewClientWithConfig(cfg),
		model:       model,
		maxTokens:   maxTokens,
		temperature: temperature,
	}
}

// GenerateMelody calls the OpenAI API and returns cleaned ABC notation.
func (g *OpenAIGenerator) GenerateMelody(ctx context.Context, prompt string) (string, error) {
	req := goopenai.ChatCompletionRequest{
		Model: g.model,
		Messages: []goopenai.ChatCompletionMessage{
			{Role: goopenai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: goopenai.ChatMessageRoleUser, Content: prompt},
		},
		MaxTokens:   g.maxTokens,
		Temperature: float32(g.temperature),
		N:           1,
	}

	resp, err := g.client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("OpenAI API call failed: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", ErrEmptyResponse
	}

	raw := resp.Choices[0].Message.Content
	return cleanABCResponse(raw)
}

// cleanABCResponse strips markdown fences and validates the result contains ABC notation.
func cleanABCResponse(raw string) (string, error) {
	// Step 1: trim leading/trailing whitespace.
	s := strings.TrimSpace(raw)

	if s == "" {
		return "", ErrEmptyResponse
	}

	// Step 2: remove opening fence line if present.
	s = openFenceRE.ReplaceAllString(s, "")

	// Step 3: remove closing fence line if present.
	s = closeFenceRE.ReplaceAllString(s, "")

	// Step 4: trim whitespace again.
	s = strings.TrimSpace(s)

	if s == "" {
		return "", ErrEmptyResponse
	}

	// Step 5: verify the result contains a line starting with "X:" (ABC tune header).
	if !containsXHeader(s) {
		return "", ErrMalformedABC
	}

	return s, nil
}

// containsXHeader reports whether s contains a line beginning with "X:".
func containsXHeader(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "X:") {
			return true
		}
	}
	return false
}
