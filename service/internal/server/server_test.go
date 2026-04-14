package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"music-generator/internal/config"
	"music-generator/internal/server"
	"music-generator/internal/store"
)

// ---------------------------------------------------------------------------
// Mock Generator
// ---------------------------------------------------------------------------

// mockGenerator implements openai.Generator for handler tests.
type mockGenerator struct {
	result string
	err    error
}

func (m *mockGenerator) GenerateMelody(_ context.Context, _ string) (string, error) {
	return m.result, m.err
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// testServer builds a Server with the given mock store and generator, sets up
// routes, and returns the http.Handler.
func testServer(t *testing.T, ms *store.MockStore, gen *mockGenerator) http.Handler {
	t.Helper()
	cfg := &config.Config{
		Port:        "8080",
		Version:     "v1.0.0-test",
		Environment: "test",
	}
	s := server.New(cfg, ms, gen)
	return s.SetupRoutes()
}

// do sends a request to the handler and returns the recorder.
func do(handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// decodeJSON decodes the response body into v. Fails the test on error.
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, v interface{}) {
	t.Helper()
	if err := json.NewDecoder(rec.Body).Decode(v); err != nil {
		t.Fatalf("failed to decode JSON response: %v\nbody: %s", err, rec.Body.String())
	}
}

// assertStatus fails the test if the response status does not match.
func assertStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Errorf("status: got %d, want %d\nbody: %s", rec.Code, want, rec.Body.String())
	}
}

// seedMelody creates one melody in the mock store and returns it.
func seedMelody(t *testing.T, ms *store.MockStore, title, prompt, abc string) store.Melody {
	t.Helper()
	m, err := ms.CreateMelody(context.Background(), store.Melody{
		Title:       title,
		Prompt:      prompt,
		ABCNotation: abc,
	})
	if err != nil {
		t.Fatalf("seedMelody: %v", err)
	}
	return m
}

// ---------------------------------------------------------------------------
// §1 Routing smoke tests — correct status for each route + method combo,
// including wrong-method requests.
// ---------------------------------------------------------------------------

func TestRouting_SmokeMethods(t *testing.T) {
	ms := store.NewMockStore()
	gen := &mockGenerator{result: "X:1\nK:C\n|C|", err: nil}
	h := testServer(t, ms, gen)

	// Seed a melody so ID-based routes can 200 instead of 404.
	m := seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|")
	id := m.ID

	cases := []struct {
		method string
		path   string
		want   int
	}{
		// Correct methods.
		{"GET", "/health", http.StatusOK},
		{"GET", "/api/melodies", http.StatusOK},
		{"GET", fmt.Sprintf("/api/melodies/%s", id), http.StatusOK},
		{"DELETE", fmt.Sprintf("/api/melodies/%s", id), http.StatusNoContent},

		// Wrong-method on registered routes → 405 Method Not Allowed.
		{"POST", "/health", http.StatusMethodNotAllowed},
		{"DELETE", "/health", http.StatusMethodNotAllowed},
		{"GET", "/api/generate", http.StatusMethodNotAllowed},
		{"DELETE", "/api/generate", http.StatusMethodNotAllowed},
		{"POST", "/api/melodies", http.StatusMethodNotAllowed},
		{"DELETE", "/api/melodies", http.StatusMethodNotAllowed},

		// Unknown paths → 404.
		{"GET", "/api/nonexistent", http.StatusNotFound},
		{"POST", "/api/nonexistent", http.StatusNotFound},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%s %s", tc.method, tc.path), func(t *testing.T) {
			rec := do(h, tc.method, tc.path, "")
			assertStatus(t, rec, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// GET /health
// ---------------------------------------------------------------------------

func TestHandleHealth(t *testing.T) {
	ms := store.NewMockStore()
	h := testServer(t, ms, &mockGenerator{})

	rec := do(h, "GET", "/health", "")
	assertStatus(t, rec, http.StatusOK)

	var resp map[string]string
	decodeJSON(t, rec, &resp)

	if resp["status"] != "ok" {
		t.Errorf("status: got %q, want %q", resp["status"], "ok")
	}
	if resp["version"] == "" {
		t.Error("version should be non-empty")
	}
	if resp["environment"] == "" {
		t.Error("environment should be non-empty")
	}
}

// ---------------------------------------------------------------------------
// POST /api/generate
// ---------------------------------------------------------------------------

func TestHandleGenerate(t *testing.T) {
	validABC := "X:1\nT:Test\nM:4/4\nL:1/4\nK:Cmaj\nC D E F |"

	cases := []struct {
		name       string
		body       string
		genResult  string
		genErr     error
		storeErr   error
		wantStatus int
		wantField  string // non-empty: assert field exists in response
	}{
		{
			name:       "valid prompt → 201 with melody",
			body:       `{"prompt":"Create a Bach melody"}`,
			genResult:  validABC,
			wantStatus: http.StatusCreated,
			wantField:  "id",
		},
		{
			name:       "empty prompt → 400",
			body:       `{"prompt":""}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "whitespace-only prompt → 400",
			body:       `{"prompt":"   "}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing prompt field → 400",
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "prompt over 2000 chars → 400",
			body:       fmt.Sprintf(`{"prompt":"%s"}`, strings.Repeat("a", 2001)),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "malformed JSON → 400",
			body:       `{"prompt":`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "OpenAI failure → 502",
			body:       `{"prompt":"valid prompt"}`,
			genErr:     errors.New("openai unavailable"),
			wantStatus: http.StatusBadGateway,
		},
		{
			name:       "store save failure → 500",
			body:       `{"prompt":"valid prompt"}`,
			genResult:  validABC,
			storeErr:   errors.New("firestore write failed"),
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMockStore()
			if tc.storeErr != nil {
				ms.ErrorOnCreate = tc.storeErr
			}
			gen := &mockGenerator{result: tc.genResult, err: tc.genErr}
			h := testServer(t, ms, gen)

			rec := do(h, "POST", "/api/generate", tc.body)
			assertStatus(t, rec, tc.wantStatus)

			if tc.wantField != "" {
				var resp map[string]interface{}
				decodeJSON(t, rec, &resp)
				if _, ok := resp[tc.wantField]; !ok {
					t.Errorf("response missing field %q", tc.wantField)
				}
			}
		})
	}
}

func TestHandleGenerate_ResponseShape(t *testing.T) {
	validABC := "X:1\nT:Test\nM:4/4\nL:1/4\nK:Cmaj\nC D E F |"
	ms := store.NewMockStore()
	gen := &mockGenerator{result: validABC}
	h := testServer(t, ms, gen)

	rec := do(h, "POST", "/api/generate", `{"prompt":"Create a melody"}`)
	assertStatus(t, rec, http.StatusCreated)

	var m store.Melody
	decodeJSON(t, rec, &m)

	if m.ID == "" {
		t.Error("id should be non-empty")
	}
	if m.Title == "" {
		t.Error("title should be non-empty")
	}
	if m.Prompt != "Create a melody" {
		t.Errorf("prompt: got %q", m.Prompt)
	}
	if m.ABCNotation != validABC {
		t.Errorf("abc_notation: got %q", m.ABCNotation)
	}
	if m.CreatedAt.IsZero() {
		t.Error("created_at should be non-zero")
	}
	if m.UpdatedAt.IsZero() {
		t.Error("updated_at should be non-zero")
	}
}

func TestHandleGenerate_TitleDerivation(t *testing.T) {
	validABC := "X:1\nK:C\n|C|"
	cases := []struct {
		name      string
		prompt    string
		wantTitle string
	}{
		{
			name:      "short prompt becomes title",
			prompt:    "Create a melody",
			wantTitle: "Create a melody",
		},
		{
			name:      "prompt capped at 50 chars",
			prompt:    strings.Repeat("A", 51),
			wantTitle: strings.Repeat("A", 50),
		},
		{
			name:      "multi-line prompt uses first line",
			prompt:    "First line\nSecond line",
			wantTitle: "First line",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMockStore()
			gen := &mockGenerator{result: validABC}
			h := testServer(t, ms, gen)

			body := fmt.Sprintf(`{"prompt":%q}`, tc.prompt)
			rec := do(h, "POST", "/api/generate", body)
			assertStatus(t, rec, http.StatusCreated)

			var m store.Melody
			decodeJSON(t, rec, &m)

			if m.Title != tc.wantTitle {
				t.Errorf("title: got %q, want %q", m.Title, tc.wantTitle)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// GET /api/melodies
// ---------------------------------------------------------------------------

func TestHandleListMelodies(t *testing.T) {
	t.Run("empty list returns 200 with empty array", func(t *testing.T) {
		ms := store.NewMockStore()
		h := testServer(t, ms, &mockGenerator{})

		rec := do(h, "GET", "/api/melodies", "")
		assertStatus(t, rec, http.StatusOK)

		// Must decode to a slice (not null).
		var list []store.Melody
		decodeJSON(t, rec, &list)
		if list == nil {
			// JSON null decodes to nil; empty array [] decodes to non-nil empty slice.
			// Per spec: must be [].
			t.Error("expected non-null JSON array, got null")
		}
		if len(list) != 0 {
			t.Errorf("expected empty list, got %d items", len(list))
		}
	})

	t.Run("single element list", func(t *testing.T) {
		ms := store.NewMockStore()
		seedMelody(t, ms, "Solo", "p", "X:1\nK:C\n|C|")
		h := testServer(t, ms, &mockGenerator{})

		rec := do(h, "GET", "/api/melodies", "")
		assertStatus(t, rec, http.StatusOK)

		var list []store.Melody
		decodeJSON(t, rec, &list)
		if len(list) != 1 {
			t.Fatalf("expected 1 item, got %d", len(list))
		}
		if list[0].Title != "Solo" {
			t.Errorf("title: got %q", list[0].Title)
		}
	})

	t.Run("returns list sorted by created_at desc", func(t *testing.T) {
		ms := store.NewMockStore()
		seedMelody(t, ms, "First", "p", "X:1\nK:C\n|C|")
		seedMelody(t, ms, "Second", "p", "X:1\nK:C\n|C|")
		seedMelody(t, ms, "Third", "p", "X:1\nK:C\n|C|")
		h := testServer(t, ms, &mockGenerator{})

		rec := do(h, "GET", "/api/melodies", "")
		assertStatus(t, rec, http.StatusOK)

		var list []store.Melody
		decodeJSON(t, rec, &list)
		if len(list) != 3 {
			t.Fatalf("expected 3 items, got %d", len(list))
		}
		if list[0].Title != "Third" {
			t.Errorf("first item should be most recent, got %q", list[0].Title)
		}
	})

	t.Run("store failure → 500", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnList = errors.New("store down")
		h := testServer(t, ms, &mockGenerator{})

		rec := do(h, "GET", "/api/melodies", "")
		assertStatus(t, rec, http.StatusInternalServerError)
	})
}

// Verify GET /api/melodies returns a bare JSON array, not a wrapped object.
func TestHandleListMelodies_JSONWireFormat(t *testing.T) {
	ms := store.NewMockStore()
	seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|")
	h := testServer(t, ms, &mockGenerator{})

	rec := do(h, "GET", "/api/melodies", "")

	body := strings.TrimSpace(rec.Body.String())
	if !strings.HasPrefix(body, "[") {
		t.Errorf("expected bare JSON array starting with '[', got: %s", body[:min(50, len(body))])
	}
}

// ---------------------------------------------------------------------------
// GET /api/melodies/{id}
// ---------------------------------------------------------------------------

func TestHandleGetMelody(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(*store.MockStore) string // returns ID to use
		wantStatus int
	}{
		{
			name: "valid ID → 200",
			setup: func(ms *store.MockStore) string {
				m := seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|")
				return m.ID
			},
			wantStatus: http.StatusOK,
		},
		{
			name: "not found → 404",
			setup: func(ms *store.MockStore) string {
				return "nonexistent-id"
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "store failure → 500",
			setup: func(ms *store.MockStore) string {
				m := seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|")
				ms.ErrorOnGet = errors.New("transient error")
				return m.ID
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMockStore()
			h := testServer(t, ms, &mockGenerator{})
			id := tc.setup(ms)

			rec := do(h, "GET", "/api/melodies/"+id, "")
			assertStatus(t, rec, tc.wantStatus)

			if tc.wantStatus == http.StatusOK {
				var m store.Melody
				decodeJSON(t, rec, &m)
				if m.ID == "" {
					t.Error("id should be non-empty in 200 response")
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// PUT /api/melodies/{id}
// ---------------------------------------------------------------------------

func TestHandleUpdateMelody(t *testing.T) {
	newStr := func(s string) *string { return &s }
	_ = newStr

	cases := []struct {
		name       string
		setup      func(*store.MockStore) string
		body       string
		wantStatus int
		checkBody  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name: "valid title update → 200",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "Old Title", "p", "X:1\nK:C\n|C|").ID
			},
			body:       `{"title":"New Title"}`,
			wantStatus: http.StatusOK,
			checkBody: func(t *testing.T, rec *httptest.ResponseRecorder) {
				var m store.Melody
				decodeJSON(t, rec, &m)
				if m.Title != "New Title" {
					t.Errorf("title: got %q, want %q", m.Title, "New Title")
				}
			},
		},
		{
			name: "valid abc_notation update → 200",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       `{"abc_notation":"X:2\nK:G\n|G|"}`,
			wantStatus: http.StatusOK,
		},
		{
			name: "both fields updated → 200",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       `{"title":"Updated","abc_notation":"X:2\nK:G\n|G|"}`,
			wantStatus: http.StatusOK,
		},
		{
			name: "not found → 404",
			setup: func(ms *store.MockStore) string {
				return "ghost-id"
			},
			body:       `{"title":"New"}`,
			wantStatus: http.StatusNotFound,
		},
		{
			name: "empty body {} → 400",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       `{}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "empty string title (after trim) treated as not provided; no abc → 400",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       `{"title":"   "}`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "title > 200 chars → 400",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       fmt.Sprintf(`{"title":"%s"}`, strings.Repeat("X", 201)),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "abc_notation > 20000 chars → 400",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       fmt.Sprintf(`{"abc_notation":"%s"}`, strings.Repeat("A", 20001)),
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "malformed JSON → 400",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
			},
			body:       `{"title":`,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "prompt in body is silently ignored; title update still succeeds",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "T", "orig prompt", "X:1\nK:C\n|C|").ID
			},
			body:       `{"title":"New","prompt":"ignored prompt"}`,
			wantStatus: http.StatusOK,
		},
		{
			name: "store failure → 500",
			setup: func(ms *store.MockStore) string {
				id := seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
				ms.ErrorOnUpdate = errors.New("write failed")
				return id
			},
			body:       `{"title":"x"}`,
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMockStore()
			h := testServer(t, ms, &mockGenerator{})
			id := tc.setup(ms)

			rec := do(h, "PUT", "/api/melodies/"+id, tc.body)
			assertStatus(t, rec, tc.wantStatus)

			if tc.checkBody != nil {
				tc.checkBody(t, rec)
			}
		})
	}
}

// Verify prompt is immutable: prompt field in response matches original after title update.
func TestHandleUpdateMelody_PromptImmutable(t *testing.T) {
	ms := store.NewMockStore()
	orig := seedMelody(t, ms, "T", "original prompt", "X:1\nK:C\n|C|")
	h := testServer(t, ms, &mockGenerator{})

	rec := do(h, "PUT", "/api/melodies/"+orig.ID, `{"title":"New","prompt":"should be ignored"}`)
	assertStatus(t, rec, http.StatusOK)

	var m store.Melody
	decodeJSON(t, rec, &m)
	if m.Prompt != "original prompt" {
		t.Errorf("prompt should be immutable: got %q, want %q", m.Prompt, "original prompt")
	}
}

// ---------------------------------------------------------------------------
// POST /api/melodies/{id}/duplicate
// ---------------------------------------------------------------------------

func TestHandleDuplicateMelody(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(*store.MockStore) string
		wantStatus int
		checkBody  func(*testing.T, *httptest.ResponseRecorder, store.Melody)
	}{
		{
			name: "valid ID → 201 with copy suffix",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "My Melody", "p", "X:1\nK:C\n|C|").ID
			},
			wantStatus: http.StatusCreated,
			checkBody: func(t *testing.T, rec *httptest.ResponseRecorder, src store.Melody) {
				var dup store.Melody
				decodeJSON(t, rec, &dup)
				if dup.ID == src.ID {
					t.Error("duplicate should have a new ID")
				}
				wantTitle := src.Title + " (copy)"
				if dup.Title != wantTitle {
					t.Errorf("title: got %q, want %q", dup.Title, wantTitle)
				}
				if dup.Prompt != src.Prompt {
					t.Errorf("prompt: got %q, want %q", dup.Prompt, src.Prompt)
				}
			},
		},
		{
			name: "not found → 404",
			setup: func(ms *store.MockStore) string {
				return "nonexistent-id"
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "store failure → 500",
			setup: func(ms *store.MockStore) string {
				id := seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
				ms.ErrorOnDuplicate = errors.New("store error")
				return id
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMockStore()
			h := testServer(t, ms, &mockGenerator{})
			id := tc.setup(ms)

			// Get the source melody before duplicating (if it exists).
			var src store.Melody
			if existing, err := ms.GetMelody(context.Background(), id); err == nil {
				src = existing
			}

			rec := do(h, "POST", "/api/melodies/"+id+"/duplicate", "")
			assertStatus(t, rec, tc.wantStatus)

			if tc.checkBody != nil {
				tc.checkBody(t, rec, src)
			}
		})
	}
}

func TestHandleDuplicateMelody_DoubleCopy(t *testing.T) {
	ms := store.NewMockStore()
	h := testServer(t, ms, &mockGenerator{})

	orig := seedMelody(t, ms, "Foo (copy)", "p", "X:1\nK:C\n|C|")

	rec := do(h, "POST", "/api/melodies/"+orig.ID+"/duplicate", "")
	assertStatus(t, rec, http.StatusCreated)

	var dup store.Melody
	decodeJSON(t, rec, &dup)

	// Per §9.4: suffix re-applied intentionally.
	if dup.Title != "Foo (copy) (copy)" {
		t.Errorf("title: got %q, want %q", dup.Title, "Foo (copy) (copy)")
	}
}

// ---------------------------------------------------------------------------
// DELETE /api/melodies/{id}
// ---------------------------------------------------------------------------

func TestHandleDeleteMelody(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(*store.MockStore) string
		wantStatus int
	}{
		{
			name: "valid ID → 204 no content",
			setup: func(ms *store.MockStore) string {
				return seedMelody(t, ms, "Del", "p", "X:1\nK:C\n|C|").ID
			},
			wantStatus: http.StatusNoContent,
		},
		{
			name: "not found → 404",
			setup: func(ms *store.MockStore) string {
				return "ghost-id"
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "double delete → 404 on second",
			setup: func(ms *store.MockStore) string {
				m := seedMelody(t, ms, "Del", "p", "X:1\nK:C\n|C|")
				_ = ms.DeleteMelody(context.Background(), m.ID)
				return m.ID
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "store failure → 500",
			setup: func(ms *store.MockStore) string {
				id := seedMelody(t, ms, "T", "p", "X:1\nK:C\n|C|").ID
				ms.ErrorOnDelete = errors.New("delete failed")
				return id
			},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ms := store.NewMockStore()
			h := testServer(t, ms, &mockGenerator{})
			id := tc.setup(ms)

			rec := do(h, "DELETE", "/api/melodies/"+id, "")
			assertStatus(t, rec, tc.wantStatus)

			if tc.wantStatus == http.StatusNoContent && rec.Body.Len() != 0 {
				t.Errorf("expected empty body for 204, got: %q", rec.Body.String())
			}
		})
	}
}

// Verify that after a successful DELETE the melody is no longer retrievable.
func TestHandleDeleteMelody_MelodyGone(t *testing.T) {
	ms := store.NewMockStore()
	h := testServer(t, ms, &mockGenerator{})
	m := seedMelody(t, ms, "Gone", "p", "X:1\nK:C\n|C|")

	rec := do(h, "DELETE", "/api/melodies/"+m.ID, "")
	assertStatus(t, rec, http.StatusNoContent)

	// Now GET should 404.
	rec2 := do(h, "GET", "/api/melodies/"+m.ID, "")
	assertStatus(t, rec2, http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// Index handler
// ---------------------------------------------------------------------------

func TestHandleIndex(t *testing.T) {
	ms := store.NewMockStore()
	h := testServer(t, ms, &mockGenerator{})

	rec := do(h, "GET", "/", "")
	assertStatus(t, rec, http.StatusOK)

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html", ct)
	}
}

// ---------------------------------------------------------------------------
// Edge case: GET /{$} must not swallow method-mismatch 405 from API routes.
// ---------------------------------------------------------------------------

func TestRouting_IndexDoesNotSwallow405(t *testing.T) {
	ms := store.NewMockStore()
	h := testServer(t, ms, &mockGenerator{})

	// POST to /api/melodies (should 405, not fall through to index and 200)
	rec := do(h, "POST", "/api/melodies", "")
	assertStatus(t, rec, http.StatusMethodNotAllowed)

	// DELETE on /health should 405 not 200.
	rec2 := do(h, "DELETE", "/health", "")
	assertStatus(t, rec2, http.StatusMethodNotAllowed)
}

// ---------------------------------------------------------------------------
// min helper (Go 1.21+ has built-in min, but target is 1.22 so fine)
// ---------------------------------------------------------------------------

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
