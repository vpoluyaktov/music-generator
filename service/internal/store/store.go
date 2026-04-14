package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrNotFound is returned when a requested document does not exist.
var ErrNotFound = errors.New("melody not found")

// Melody is the in-memory representation of a document in the `melodies` collection.
type Melody struct {
	ID          string    `json:"id"           firestore:"-"`
	Title       string    `json:"title"        firestore:"title"`
	Prompt      string    `json:"prompt"       firestore:"prompt"`
	ABCNotation string    `json:"abc_notation" firestore:"abc_notation"`
	CreatedAt   time.Time `json:"created_at"   firestore:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"   firestore:"updated_at"`
}

// MelodyUpdate carries the partial-update payload for UpdateMelody.
// A nil pointer means "leave this field unchanged".
type MelodyUpdate struct {
	Title       *string
	ABCNotation *string
}

// Store is the persistence-layer contract. All methods honour the provided context.
type Store interface {
	// CreateMelody persists a new Melody. The implementation assigns ID, CreatedAt,
	// and UpdatedAt. The returned Melody reflects the stored document.
	CreateMelody(ctx context.Context, m Melody) (Melody, error)

	// ListMelodies returns all melodies ordered by CreatedAt descending.
	// An empty collection yields a non-nil zero-length slice.
	ListMelodies(ctx context.Context) ([]Melody, error)

	// GetMelody returns a single melody by ID. Returns ErrNotFound if missing.
	GetMelody(ctx context.Context, id string) (Melody, error)

	// UpdateMelody applies the partial update. The implementation sets UpdatedAt
	// to time.Now().UTC(). Returns ErrNotFound if the ID does not exist.
	UpdateMelody(ctx context.Context, id string, upd MelodyUpdate) (Melody, error)

	// DuplicateMelody reads the source document, creates a new document with the
	// same prompt and abc_notation, a title suffixed with " (copy)" (truncated to
	// 200 chars if needed), and fresh timestamps. Returns ErrNotFound if the
	// source ID does not exist.
	DuplicateMelody(ctx context.Context, id string) (Melody, error)

	// DeleteMelody removes a melody by ID. Returns ErrNotFound if missing.
	DeleteMelody(ctx context.Context, id string) error

	// DeleteAllMelodies removes every document in the collection. This is used
	// by tests and admin tooling only; it MUST use firestore.BulkWriter (never
	// the deprecated Batch()). Returns the number of documents deleted.
	DeleteAllMelodies(ctx context.Context) (int, error)

	// Close releases any underlying client resources.
	Close() error
}

// FirestoreStore implements Store using Google Cloud Firestore.
type FirestoreStore struct {
	client *firestore.Client
}

// NewFirestoreStore creates a new Firestore-backed store.
func NewFirestoreStore(ctx context.Context, projectID, databaseName string) (*FirestoreStore, error) {
	client, err := firestore.NewClientWithDatabase(ctx, projectID, databaseName)
	if err != nil {
		return nil, fmt.Errorf("failed to create Firestore client: %w", err)
	}
	return &FirestoreStore{client: client}, nil
}

// CreateMelody persists a new melody with an auto-generated Firestore document ID.
func (s *FirestoreStore) CreateMelody(ctx context.Context, m Melody) (Melody, error) {
	now := time.Now().UTC()
	m.CreatedAt = now
	m.UpdatedAt = now

	ref := s.client.Collection("melodies").NewDoc()
	_, err := ref.Create(ctx, m)
	if err != nil {
		return Melody{}, fmt.Errorf("failed to create melody: %w", err)
	}
	m.ID = ref.ID
	return m, nil
}

// ListMelodies returns all melodies ordered by created_at descending.
func (s *FirestoreStore) ListMelodies(ctx context.Context) ([]Melody, error) {
	iter := s.client.Collection("melodies").OrderBy("created_at", firestore.Desc).Documents(ctx)
	defer iter.Stop()

	melodies := make([]Melody, 0)
	for {
		doc, err := iter.Next()
		if err != nil {
			// iterator exhausted
			break
		}
		var m Melody
		if err := doc.DataTo(&m); err != nil {
			return nil, fmt.Errorf("failed to decode melody %s: %w", doc.Ref.ID, err)
		}
		m.ID = doc.Ref.ID
		melodies = append(melodies, m)
	}
	return melodies, nil
}

// GetMelody returns a single melody by ID.
func (s *FirestoreStore) GetMelody(ctx context.Context, id string) (Melody, error) {
	doc, err := s.client.Collection("melodies").Doc(id).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return Melody{}, ErrNotFound
		}
		return Melody{}, fmt.Errorf("failed to get melody: %w", err)
	}
	var m Melody
	if err := doc.DataTo(&m); err != nil {
		return Melody{}, fmt.Errorf("failed to decode melody: %w", err)
	}
	m.ID = doc.Ref.ID
	return m, nil
}

// UpdateMelody applies a partial update to an existing melody.
func (s *FirestoreStore) UpdateMelody(ctx context.Context, id string, upd MelodyUpdate) (Melody, error) {
	ref := s.client.Collection("melodies").Doc(id)

	updates := []firestore.Update{
		{Path: "updated_at", Value: time.Now().UTC()},
	}
	if upd.Title != nil {
		updates = append(updates, firestore.Update{Path: "title", Value: *upd.Title})
	}
	if upd.ABCNotation != nil {
		updates = append(updates, firestore.Update{Path: "abc_notation", Value: *upd.ABCNotation})
	}

	_, err := ref.Update(ctx, updates)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return Melody{}, ErrNotFound
		}
		return Melody{}, fmt.Errorf("failed to update melody: %w", err)
	}

	return s.GetMelody(ctx, id)
}

// DuplicateMelody creates a copy of an existing melody with " (copy)" appended to the title.
func (s *FirestoreStore) DuplicateMelody(ctx context.Context, id string) (Melody, error) {
	src, err := s.GetMelody(ctx, id)
	if err != nil {
		return Melody{}, err
	}

	newTitle := buildCopyTitle(src.Title)

	newMelody := Melody{
		Title:       newTitle,
		Prompt:      src.Prompt,
		ABCNotation: src.ABCNotation,
	}
	return s.CreateMelody(ctx, newMelody)
}

// DeleteMelody removes a melody by ID.
func (s *FirestoreStore) DeleteMelody(ctx context.Context, id string) error {
	ref := s.client.Collection("melodies").Doc(id)

	// First verify existence.
	_, err := ref.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return ErrNotFound
		}
		return fmt.Errorf("failed to check melody existence: %w", err)
	}

	_, err = ref.Delete(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete melody: %w", err)
	}
	return nil
}

// DeleteAllMelodies removes every document in the melodies collection using BulkWriter.
func (s *FirestoreStore) DeleteAllMelodies(ctx context.Context) (int, error) {
	iter := s.client.Collection("melodies").Documents(ctx)
	defer iter.Stop()

	bw := s.client.BulkWriter(ctx)
	count := 0
	for {
		doc, err := iter.Next()
		if err != nil {
			break
		}
		if _, err := bw.Delete(doc.Ref); err != nil {
			return count, fmt.Errorf("failed to queue delete for %s: %w", doc.Ref.ID, err)
		}
		count++
	}
	bw.End()
	return count, nil
}

// Close closes the Firestore client connection.
func (s *FirestoreStore) Close() error {
	return s.client.Close()
}

// buildCopyTitle appends " (copy)" to a title, truncating to 200 chars on a rune boundary.
func buildCopyTitle(src string) string {
	const copySuffix = " (copy)"
	const maxTitle = 200

	newTitle := src + copySuffix
	if len(newTitle) <= maxTitle {
		return newTitle
	}

	budget := maxTitle - len(copySuffix)
	if budget < 0 {
		budget = 0
	}
	base := truncateRunes(src, budget)
	return base + copySuffix
}

// truncateRunes truncates s to at most maxBytes bytes, aligned to a rune boundary.
func truncateRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Walk back from maxBytes to find a valid rune start.
	for i := maxBytes; i > 0; i-- {
		if isRuneStart(s[i]) {
			return s[:i]
		}
	}
	return s[:maxBytes]
}

// isRuneStart reports whether b is the first byte of a UTF-8 encoded rune.
func isRuneStart(b byte) bool {
	// Continuation bytes are 10xxxxxx (0x80–0xBF).
	return b&0xC0 != 0x80
}
