package config

import (
	"os"
	"strconv"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	Port                string
	Version             string
	Environment         string
	ProjectID           string
	FirestoreDatabase   string
	OpenAIAPIKey        string
	OpenAIModel         string
	OpenAIMaxTokens     int
	OpenAITemperature   float64
}

// Load reads environment variables and returns a populated Config.
func Load() *Config {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	version := os.Getenv("APP_VERSION")
	if version == "" {
		version = "dev"
	}

	env := os.Getenv("ENVIRONMENT")
	if env == "" {
		env = "local"
	}

	firestoreDB := os.Getenv("FIRESTORE_DATABASE")
	if firestoreDB == "" {
		firestoreDB = "music-generator"
	}

	model := os.Getenv("OPENAI_MODEL")
	if model == "" {
		model = "gpt-5.2"
	}

	maxTokens := 2000
	if v := os.Getenv("OPENAI_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTokens = n
		}
	}

	temperature := 0.8
	if v := os.Getenv("OPENAI_TEMPERATURE"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			temperature = f
		}
	}

	return &Config{
		Port:              port,
		Version:           version,
		Environment:       env,
		ProjectID:         os.Getenv("GCP_PROJECT_ID"),
		FirestoreDatabase: firestoreDB,
		OpenAIAPIKey:      os.Getenv("OPENAI_API_KEY"),
		OpenAIModel:       model,
		OpenAIMaxTokens:   maxTokens,
		OpenAITemperature: temperature,
	}
}
