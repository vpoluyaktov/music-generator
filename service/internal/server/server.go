package server

import (
	"html/template"
	"net/http"

	"music-generator/internal/config"
	"music-generator/internal/openai"
	"music-generator/internal/store"
	"music-generator/internal/templates"
)

// Server holds shared dependencies for all HTTP handlers.
type Server struct {
	cfg       *config.Config
	store     store.Store
	gen       openai.Generator
	indexTmpl *template.Template
}

// New constructs a Server. The HTML template is parsed once at construction time;
// a parse error is fatal.
func New(cfg *config.Config, st store.Store, gen openai.Generator) *Server {
	tmpl := template.Must(template.ParseFS(templates.FS, "index.html"))
	return &Server{
		cfg:       cfg,
		store:     st,
		gen:       gen,
		indexTmpl: tmpl,
	}
}

// SetupRoutes registers all routes and returns the handler chain.
// Uses Go 1.22 method+pattern routing.
func (s *Server) SetupRoutes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /api/generate", s.handleGenerate)
	mux.HandleFunc("GET /api/melodies", s.handleListMelodies)
	mux.HandleFunc("GET /api/melodies/{id}", s.handleGetMelody)
	mux.HandleFunc("PUT /api/melodies/{id}", s.handleUpdateMelody)
	mux.HandleFunc("POST /api/melodies/{id}/duplicate", s.handleDuplicateMelody)
	mux.HandleFunc("DELETE /api/melodies/{id}", s.handleDeleteMelody)
	// Exact match for root — prevents swallowing 405s from API routes.
	mux.HandleFunc("GET /{$}", s.handleIndex)

	return recoverMW(logMW(mux))
}
