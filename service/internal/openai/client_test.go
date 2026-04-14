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

// ---------------------------------------------------------------------------
// cleanABCResponse is unexported; we test it indirectly through the exported
// Generator interface using a mock HTTP server that returns controlled bodies.
// ---------------------------------------------------------------------------

// mockOpenAIServer builds a test HTTP server that returns a single assistant
// message with the given content string.
func mockOpenAIServer(t *testing.T, content string, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusCode != http.StatusOK {
			http.Error(w, "error", statusCode)
			return
		}
		resp := goopenai.ChatCompletionResponse{
			Choices: []goopenai.ChatCompletionChoice{
				{
					Message: goopenai.ChatCompletionMessage{
						Role:    goopenai.ChatMessageRoleAssistant,
						Content: content,
					},
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
// Response-cleaning tests (via the mock HTTP server)
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
			name:        "plain fence stripped",
			content:     "```\n" + validABC + "\n```",
			wantContain: "X:1",
		},
		{
			name:        "prose before ABC is kept but still valid",
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
			name:    "fence with only whitespace after stripping returns ErrEmptyResponse",
			content: "```abc\n   \n```",
			wantErr: oai.ErrEmptyResponse,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := mockOpenAIServer(t, tc.content, http.StatusOK)
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
	srv := mockOpenAIServer(t, "", http.StatusInternalServerError)
	defer srv.Close()

	gen := newTestGenerator(srv.URL)
	_, err := gen.GenerateMelody(context.Background(), "prompt")
	if err == nil {
		t.Fatal("expected error for HTTP 500 from OpenAI")
	}
}

func TestGenerateMelody_EmptyChoices(t *testing.T) {
	// Return a valid JSON response but with no choices.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := goopenai.ChatCompletionResponse{Choices: nil}
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

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}())
}
