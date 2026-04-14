package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	oaiclient "music-generator/internal/openai"
	"music-generator/internal/store"
)

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON encode error: %v", err)
	}
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// indexData is passed to the HTML template.
type indexData struct {
	Version     string
	Environment string
}

// healthResponse is the payload for GET /health.
type healthResponse struct {
	Status      string `json:"status"`
	Version     string `json:"version"`
	Environment string `json:"environment"`
}

// handleHealth returns a health check response.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:      "ok",
		Version:     s.cfg.Version,
		Environment: s.cfg.Environment,
	})
}

// handleIndex serves the embedded SPA.
func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := indexData{
		Version:     s.cfg.Version,
		Environment: s.cfg.Environment,
	}
	if err := s.indexTmpl.Execute(w, data); err != nil {
		log.Printf("handleIndex template execute error: %v", err)
	}
}

// generateRequest is the body for POST /api/generate.
type generateRequest struct {
	Prompt string `json:"prompt"`
}

// handleGenerate calls OpenAI and persists the resulting melody.
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var req generateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON request body")
		return
	}

	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required and must not be empty")
		return
	}
	if len(req.Prompt) > 2000 {
		writeError(w, http.StatusBadRequest, "prompt exceeds maximum length of 2000 characters")
		return
	}

	if s.gen == nil {
		writeError(w, http.StatusServiceUnavailable, "OpenAI is not configured (OPENAI_API_KEY not set)")
		return
	}
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store is not configured (GCP_PROJECT_ID not set)")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	abc, err := s.gen.GenerateMelody(ctx, req.Prompt)
	if err != nil {
		if errors.Is(err, oaiclient.ErrEmptyResponse) || errors.Is(err, oaiclient.ErrMalformedABC) {
			writeError(w, http.StatusBadGateway, "OpenAI returned invalid or empty ABC notation")
			return
		}
		log.Printf("handleGenerate OpenAI error: %v", err)
		writeError(w, http.StatusBadGateway, "failed to generate melody from OpenAI")
		return
	}

	title := deriveTitle(req.Prompt)

	m, err := s.store.CreateMelody(ctx, store.Melody{
		Title:       title,
		Prompt:      req.Prompt,
		ABCNotation: abc,
	})
	if err != nil {
		log.Printf("handleGenerate Firestore error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to save melody")
		return
	}

	writeJSON(w, http.StatusCreated, m)
}

// handleListMelodies returns all melodies sorted by created_at desc.
func (s *Server) handleListMelodies(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store is not configured (GCP_PROJECT_ID not set)")
		return
	}

	melodies, err := s.store.ListMelodies(r.Context())
	if err != nil {
		log.Printf("handleListMelodies error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to list melodies")
		return
	}

	writeJSON(w, http.StatusOK, melodies)
}

// handleGetMelody returns a single melody by ID.
func (s *Server) handleGetMelody(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "melody ID is required")
		return
	}

	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store is not configured (GCP_PROJECT_ID not set)")
		return
	}

	m, err := s.store.GetMelody(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "melody not found")
			return
		}
		log.Printf("handleGetMelody error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to get melody")
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// updateRequest is the body for PUT /api/melodies/{id}.
type updateRequest struct {
	Title       *string `json:"title"`
	ABCNotation *string `json:"abc_notation"`
}

// handleUpdateMelody applies a partial update to an existing melody.
func (s *Server) handleUpdateMelody(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "melody ID is required")
		return
	}

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed JSON request body")
		return
	}

	// Trim title if provided; treat empty string as not provided.
	if req.Title != nil {
		trimmed := strings.TrimSpace(*req.Title)
		if trimmed == "" {
			req.Title = nil
		} else {
			if len(trimmed) > 200 {
				writeError(w, http.StatusBadRequest, "title exceeds maximum length of 200 characters")
				return
			}
			req.Title = &trimmed
		}
	}

	// Validate abc_notation length if provided.
	if req.ABCNotation != nil && len(*req.ABCNotation) > 20000 {
		writeError(w, http.StatusBadRequest, "abc_notation exceeds maximum length of 20000 characters")
		return
	}

	if req.Title == nil && req.ABCNotation == nil {
		writeError(w, http.StatusBadRequest, "at least one of title or abc_notation must be provided")
		return
	}

	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store is not configured (GCP_PROJECT_ID not set)")
		return
	}

	upd := store.MelodyUpdate{
		Title:       req.Title,
		ABCNotation: req.ABCNotation,
	}

	m, err := s.store.UpdateMelody(r.Context(), id, upd)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "melody not found")
			return
		}
		log.Printf("handleUpdateMelody error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to update melody")
		return
	}

	writeJSON(w, http.StatusOK, m)
}

// handleDuplicateMelody creates a copy of an existing melody.
func (s *Server) handleDuplicateMelody(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "melody ID is required")
		return
	}

	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store is not configured (GCP_PROJECT_ID not set)")
		return
	}

	m, err := s.store.DuplicateMelody(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "melody not found")
			return
		}
		log.Printf("handleDuplicateMelody error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to duplicate melody")
		return
	}

	writeJSON(w, http.StatusCreated, m)
}

// handleDeleteMelody permanently deletes a melody.
func (s *Server) handleDeleteMelody(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "melody ID is required")
		return
	}

	if s.store == nil {
		writeError(w, http.StatusServiceUnavailable, "store is not configured (GCP_PROJECT_ID not set)")
		return
	}

	err := s.store.DeleteMelody(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "melody not found")
			return
		}
		log.Printf("handleDeleteMelody error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to delete melody")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// deriveTitle extracts the first non-empty line of the prompt and caps it at 50 bytes
// on a rune boundary. Returns "Untitled melody" if no suitable line is found.
func deriveTitle(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 50 {
			line = truncateRunesSafe(line, 50)
		}
		if line != "" {
			return line
		}
	}
	return "Untitled melody"
}

// truncateRunesSafe truncates s to at most maxBytes bytes, aligned to a rune boundary.
func truncateRunesSafe(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk backwards from maxBytes to find a valid rune start.
	for i := maxBytes; i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i]
		}
	}
	return s[:maxBytes]
}
