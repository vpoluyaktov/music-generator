package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"music-generator/internal/config"
	oaiclient "music-generator/internal/openai"
	"music-generator/internal/server"
	"music-generator/internal/store"
)

func main() {
	cfg := config.Load()

	var st store.Store
	if cfg.ProjectID != "" {
		ctx := context.Background()
		fs, err := store.NewFirestoreStore(ctx, cfg.ProjectID, cfg.FirestoreDatabase)
		if err != nil {
			log.Fatalf("Failed to initialize Firestore: %v", err)
		}
		defer fs.Close() //nolint:errcheck
		st = fs
		log.Printf("Firestore connected: project=%s database=%s", cfg.ProjectID, cfg.FirestoreDatabase)
	} else {
		log.Println("GCP_PROJECT_ID not set — running without Firestore")
	}

	var gen oaiclient.Generator
	if cfg.OpenAIAPIKey != "" {
		gen = oaiclient.NewOpenAIGenerator(cfg.OpenAIAPIKey, cfg.OpenAIModel, cfg.OpenAIMaxTokens, cfg.OpenAITemperature)
		log.Printf("OpenAI configured: model=%s", cfg.OpenAIModel)
	} else {
		log.Println("OPENAI_API_KEY not set — POST /api/generate will return 503")
	}

	srv := server.New(cfg, st, gen)
	handler := srv.SetupRoutes()

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.Port),
		Handler: handler,
	}

	log.Printf("Starting music-generator service version=%s env=%s port=%s", cfg.Version, cfg.Environment, cfg.Port)

	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}
	log.Println("Server exited")
}
