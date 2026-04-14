package config_test

import (
	"os"
	"testing"

	"music-generator/internal/config"
)

// setenv sets env vars for a test and restores originals via t.Cleanup.
func setenv(t *testing.T, pairs ...string) {
	t.Helper()
	if len(pairs)%2 != 0 {
		t.Fatal("setenv requires key-value pairs")
	}
	for i := 0; i < len(pairs); i += 2 {
		key, val := pairs[i], pairs[i+1]
		orig, had := os.LookupEnv(key)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, orig)
			} else {
				_ = os.Unsetenv(key)
			}
		})
		_ = os.Setenv(key, val)
	}
}

// unsetenv removes env vars for a test and restores them via t.Cleanup.
func unsetenv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		orig, had := os.LookupEnv(key)
		t.Cleanup(func() {
			if had {
				_ = os.Setenv(key, orig)
			} else {
				_ = os.Unsetenv(key)
			}
		})
		_ = os.Unsetenv(key)
	}
}

// TestLoad_Defaults verifies that default values are applied when env vars are absent.
func TestLoad_Defaults(t *testing.T) {
	unsetenv(t,
		"PORT", "APP_VERSION", "ENVIRONMENT",
		"GCP_PROJECT_ID", "FIRESTORE_DATABASE",
		"OPENAI_API_KEY", "OPENAI_MODEL",
		"OPENAI_MAX_TOKENS", "OPENAI_TEMPERATURE",
	)

	cfg := config.Load()

	cases := []struct {
		name string
		got  interface{}
		want interface{}
	}{
		{"Port", cfg.Port, "8080"},
		{"Version", cfg.Version, "dev"},
		{"Environment", cfg.Environment, "local"},
		{"ProjectID", cfg.ProjectID, ""},
		{"FirestoreDatabase", cfg.FirestoreDatabase, "music-generator"},
		{"OpenAIAPIKey", cfg.OpenAIAPIKey, ""},
		{"OpenAIModel", cfg.OpenAIModel, "gpt-4o"},
		{"OpenAIMaxTokens", cfg.OpenAIMaxTokens, 2000},
		{"OpenAITemperature", cfg.OpenAITemperature, 0.8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

// TestLoad_EnvOverrides verifies that environment variables take precedence over defaults.
func TestLoad_EnvOverrides(t *testing.T) {
	setenv(t,
		"PORT", "9090",
		"APP_VERSION", "v2.3.4",
		"ENVIRONMENT", "staging",
		"GCP_PROJECT_ID", "my-project",
		"FIRESTORE_DATABASE", "custom-db",
		"OPENAI_API_KEY", "sk-test-key",
		"OPENAI_MODEL", "gpt-3.5-turbo",
		"OPENAI_MAX_TOKENS", "500",
		"OPENAI_TEMPERATURE", "0.5",
	)

	cfg := config.Load()

	if cfg.Port != "9090" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "9090")
	}
	if cfg.Version != "v2.3.4" {
		t.Errorf("Version: got %q, want %q", cfg.Version, "v2.3.4")
	}
	if cfg.Environment != "staging" {
		t.Errorf("Environment: got %q, want %q", cfg.Environment, "staging")
	}
	if cfg.ProjectID != "my-project" {
		t.Errorf("ProjectID: got %q, want %q", cfg.ProjectID, "my-project")
	}
	if cfg.FirestoreDatabase != "custom-db" {
		t.Errorf("FirestoreDatabase: got %q, want %q", cfg.FirestoreDatabase, "custom-db")
	}
	if cfg.OpenAIAPIKey != "sk-test-key" {
		t.Errorf("OpenAIAPIKey: got %q, want %q", cfg.OpenAIAPIKey, "sk-test-key")
	}
	if cfg.OpenAIModel != "gpt-3.5-turbo" {
		t.Errorf("OpenAIModel: got %q, want %q", cfg.OpenAIModel, "gpt-3.5-turbo")
	}
	if cfg.OpenAIMaxTokens != 500 {
		t.Errorf("OpenAIMaxTokens: got %d, want %d", cfg.OpenAIMaxTokens, 500)
	}
	if cfg.OpenAITemperature != 0.5 {
		t.Errorf("OpenAITemperature: got %f, want %f", cfg.OpenAITemperature, 0.5)
	}
}

// TestLoad_PartialOverrides verifies that only the set variables are overridden.
func TestLoad_PartialOverrides(t *testing.T) {
	unsetenv(t, "PORT", "APP_VERSION", "ENVIRONMENT",
		"GCP_PROJECT_ID", "FIRESTORE_DATABASE",
		"OPENAI_API_KEY", "OPENAI_MODEL",
		"OPENAI_MAX_TOKENS", "OPENAI_TEMPERATURE",
	)
	setenv(t, "PORT", "3000", "GCP_PROJECT_ID", "proj-123")

	cfg := config.Load()

	if cfg.Port != "3000" {
		t.Errorf("Port: got %q, want %q", cfg.Port, "3000")
	}
	if cfg.ProjectID != "proj-123" {
		t.Errorf("ProjectID: got %q, want %q", cfg.ProjectID, "proj-123")
	}
	// All other values should be defaults.
	if cfg.Version != "dev" {
		t.Errorf("Version: got %q, want default %q", cfg.Version, "dev")
	}
	if cfg.OpenAIModel != "gpt-4o" {
		t.Errorf("OpenAIModel: got %q, want default %q", cfg.OpenAIModel, "gpt-4o")
	}
}

// TestLoad_InvalidNumericFallsToDefault verifies graceful handling of bad numeric env vars.
func TestLoad_InvalidNumericFallsToDefault(t *testing.T) {
	setenv(t, "OPENAI_MAX_TOKENS", "not-a-number", "OPENAI_TEMPERATURE", "not-a-float")

	cfg := config.Load()

	if cfg.OpenAIMaxTokens != 2000 {
		t.Errorf("OpenAIMaxTokens: got %d, want default %d", cfg.OpenAIMaxTokens, 2000)
	}
	if cfg.OpenAITemperature != 0.8 {
		t.Errorf("OpenAITemperature: got %f, want default %f", cfg.OpenAITemperature, 0.8)
	}
}

// TestLoad_EmptyProjectID verifies that an empty GCP_PROJECT_ID results in an empty string.
// Per ARCHITECTURE.md §7: if empty, the Firestore client is not created; the server
// returns 503 for store-dependent endpoints. The config package itself does not error.
func TestLoad_EmptyProjectID(t *testing.T) {
	unsetenv(t, "GCP_PROJECT_ID")

	cfg := config.Load()

	if cfg.ProjectID != "" {
		t.Errorf("ProjectID: got %q, want empty string", cfg.ProjectID)
	}
}

// TestLoad_EmptyOpenAIKey verifies that an empty OPENAI_API_KEY results in an empty string.
// Per ARCHITECTURE.md §7: POST /api/generate returns 503 when this is empty.
func TestLoad_EmptyOpenAIKey(t *testing.T) {
	unsetenv(t, "OPENAI_API_KEY")

	cfg := config.Load()

	if cfg.OpenAIAPIKey != "" {
		t.Errorf("OpenAIAPIKey: got %q, want empty string", cfg.OpenAIAPIKey)
	}
}
