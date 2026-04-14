package store

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockStore is an in-memory Store implementation for use in tests.
//
// Error injection: set the ErrorOn* fields before calling the method under
// test.  A non-nil value causes that method to return the error exactly once;
// the field is then reset to nil so subsequent calls succeed.
//
// Example:
//
//	ms := NewMockStore()
//	ms.ErrorOnGet = ErrNotFound
//	_, err := ms.GetMelody(ctx, "any-id") // returns ErrNotFound
//	_, err  = ms.GetMelody(ctx, "any-id") // succeeds (field cleared)
type MockStore struct {
	mu sync.RWMutex

	// Ordered insert list so ListMelodies can sort by CreatedAt desc.
	melodies []Melody

	// Error injection: set to a non-nil error before the call under test.
	ErrorOnCreate    error
	ErrorOnList      error
	ErrorOnGet       error
	ErrorOnUpdate    error
	ErrorOnDuplicate error
	ErrorOnDelete    error
	ErrorOnDeleteAll error

	// nextID is incremented on each auto-ID call for deterministic IDs.
	nextID int
}

// NewMockStore returns an empty, ready-to-use MockStore.
func NewMockStore() *MockStore {
	return &MockStore{}
}

func (m *MockStore) autoID() string {
	m.nextID++
	return fmt.Sprintf("mock-id-%04d", m.nextID)
}

// CreateMelody stores a new melody, assigning ID and timestamps.
func (m *MockStore) CreateMelody(_ context.Context, src Melody) (Melody, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ErrorOnCreate; err != nil {
		m.ErrorOnCreate = nil
		return Melody{}, err
	}

	now := time.Now().UTC()
	src.ID = m.autoID()
	src.CreatedAt = now
	src.UpdatedAt = now

	m.melodies = append(m.melodies, src)
	return src, nil
}

// ListMelodies returns all melodies sorted by CreatedAt descending.
// Always returns a non-nil slice.
func (m *MockStore) ListMelodies(_ context.Context) ([]Melody, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.ErrorOnList; err != nil {
		m.ErrorOnList = nil
		return nil, err
	}

	// Copy and sort descending by CreatedAt.
	result := make([]Melody, len(m.melodies))
	copy(result, m.melodies)

	for i := 0; i < len(result)-1; i++ {
		for j := i + 1; j < len(result); j++ {
			if result[i].CreatedAt.Before(result[j].CreatedAt) {
				result[i], result[j] = result[j], result[i]
			}
		}
	}

	return result, nil
}

// GetMelody returns the melody with the given ID, or ErrNotFound.
func (m *MockStore) GetMelody(_ context.Context, id string) (Melody, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if err := m.ErrorOnGet; err != nil {
		m.ErrorOnGet = nil
		return Melody{}, err
	}

	for _, mel := range m.melodies {
		if mel.ID == id {
			return mel, nil
		}
	}
	return Melody{}, ErrNotFound
}

// UpdateMelody applies a partial update to an existing melody.
// Returns ErrNotFound if no melody has the given ID.
func (m *MockStore) UpdateMelody(_ context.Context, id string, upd MelodyUpdate) (Melody, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ErrorOnUpdate; err != nil {
		m.ErrorOnUpdate = nil
		return Melody{}, err
	}

	for i, mel := range m.melodies {
		if mel.ID != id {
			continue
		}
		if upd.Title != nil {
			m.melodies[i].Title = *upd.Title
		}
		if upd.ABCNotation != nil {
			m.melodies[i].ABCNotation = *upd.ABCNotation
		}
		m.melodies[i].UpdatedAt = time.Now().UTC()
		return m.melodies[i], nil
	}
	return Melody{}, ErrNotFound
}

// DuplicateMelody creates a copy of the source melody with a title suffixed
// with " (copy)" (truncated to 200 bytes on a rune boundary if necessary).
func (m *MockStore) DuplicateMelody(_ context.Context, id string) (Melody, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ErrorOnDuplicate; err != nil {
		m.ErrorOnDuplicate = nil
		return Melody{}, err
	}

	var src Melody
	found := false
	for _, mel := range m.melodies {
		if mel.ID == id {
			src = mel
			found = true
			break
		}
	}
	if !found {
		return Melody{}, ErrNotFound
	}

	newTitle := buildCopyTitle(src.Title)

	now := time.Now().UTC()
	dup := Melody{
		ID:          m.autoID(),
		Title:       newTitle,
		Prompt:      src.Prompt,
		ABCNotation: src.ABCNotation,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	m.melodies = append(m.melodies, dup)
	return dup, nil
}

// DeleteMelody removes a melody by ID. Returns ErrNotFound if missing.
func (m *MockStore) DeleteMelody(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ErrorOnDelete; err != nil {
		m.ErrorOnDelete = nil
		return err
	}

	for i, mel := range m.melodies {
		if mel.ID == id {
			m.melodies = append(m.melodies[:i], m.melodies[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

// DeleteAllMelodies removes every melody. Returns count deleted.
func (m *MockStore) DeleteAllMelodies(_ context.Context) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.ErrorOnDeleteAll; err != nil {
		m.ErrorOnDeleteAll = nil
		return 0, err
	}

	count := len(m.melodies)
	m.melodies = nil
	return count, nil
}

// Close is a no-op for the mock.
func (m *MockStore) Close() error { return nil }
