package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	goopenai "github.com/sashabaranov/go-openai"

	oai "music-generator/internal/openai"
)

// mockChatServer builds a test HTTP server that responds to any request with
// a single OpenAI ChatCompletion response containing the given content.
// If statusCode != 200 the server returns that HTTP error instead.
func mockChatServer(t *testing.T, content string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			w.WriteHeader(statusCode)
			_, _ = w.Write([]byte(`{"error":{"message":"test error","type":"test"}}`))
			return
		}
		resp := goopenai.ChatCompletionResponse{
			ID:     "chatcmpl-test",
			Object: "chat.completion",
			Model:  "gpt-4o",
			Choices: []goopenai.ChatCompletionChoice{
				{
					Index: 0,
					Message: goopenai.ChatCompletionMessage{
						Role:    goopenai.ChatMessageRoleAssistant,
						Content: content,
					},
					FinishReason: goopenai.FinishReasonStop,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// newTestGenerator builds an OpenAIGenerator pointed at the given server URL.
func newTestGenerator(serverURL string) *oai.OpenAIGenerator {
	return oai.NewOpenAIGeneratorWithBaseURL("test-key", "gpt-4o", 2000, 0.8, serverURL)
}

// ---------------------------------------------------------------------------
// cleanABCResponse / response-cleaning tested via the public Generator API
// ---------------------------------------------------------------------------

func TestGenerateMelody_CleanABCResponse(t *testing.T) {
	validABC := "X:1\nT:Test\nM:4/4\nL:1/4\nK:Cmaj\nC D E F |"

	cases := []struct {
		name        string
		content     string
		wantErr     error
		wantContain string
	}{
		{
			name:        "pure ABC returned as-is",
			content:     validABC,
			wantContain: "X:1",
		},
		{
			name:        "abc fence stripped",
			content:     "```abc\n" + validABC + "\n```",
			wantContain: "X:1",
		},
		{
			name:        "plain fence stripped (no language tag)",
			content:     "```\n" + validABC + "\n```",
			wantContain: "X:1",
		},
		{
			name: "prose before ABC is kept but still valid",
			// §9.1: prose is preserved; ABC detector still finds X:
			content:     "Here is your melody:\n\n" + validABC,
			wantContain: "X:1",
		},
		{
			name:    "empty string returns ErrEmptyResponse",
			content: "",
			wantErr: oai.ErrEmptyResponse,
		},
		{
			name:    "whitespace-only returns ErrEmptyResponse",
			content: "   \n\n  ",
			wantErr: oai.ErrEmptyResponse,
		},
		{
			name:    "no X: header returns ErrMalformedABC",
			content: "Sorry, I cannot do that.",
			wantErr: oai.ErrMalformedABC,
		},
		{
			name: "fence with only whitespace content after stripping returns ErrEmptyResponse",
			// After stripping ```abc and ```, only whitespace remains.
			content: "```abc\n   \n```",
			wantErr: oai.ErrEmptyResponse,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := mockChatServer(t, tc.content, http.StatusOK)
			defer srv.Close()

			gen := newTestGenerator(srv.URL)
			result, err := gen.GenerateMelody(context.Background(), "test prompt")

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("error: got %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantContain != "" && !containsStr(result, tc.wantContain) {
				t.Errorf("result %q does not contain %q", result, tc.wantContain)
			}
		})
	}
}

func TestGenerateMelody_OpenAIHTTPError(t *testing.T) {
	srv := mockChatServer(t, "", http.StatusInternalServerError)
	defer srv.Close()

	gen := newTestGenerator(srv.URL)
	_, err := gen.GenerateMelody(context.Background(), "prompt")
	if err == nil {
		t.Fatal("expected error for HTTP 500 from OpenAI")
	}
}

func TestGenerateMelody_EmptyChoices(t *testing.T) {
	// Return valid JSON but with no choices (empty array).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := goopenai.ChatCompletionResponse{
			ID:      "chatcmpl-empty",
			Object:  "chat.completion",
			Choices: []goopenai.ChatCompletionChoice{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	gen := newTestGenerator(srv.URL)
	_, err := gen.GenerateMelody(context.Background(), "prompt")
	if !errors.Is(err, oai.ErrEmptyResponse) {
		t.Errorf("expected ErrEmptyResponse for empty choices, got %v", err)
	}
}

// containsStr is a zero-dependency substring check.
func containsStr(s, substr string) bool {
	if len(substr) == 0 {
		return true
	}
	if len(s) < len(substr) {
		return false
	}
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
