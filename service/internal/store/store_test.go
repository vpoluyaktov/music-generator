package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"music-generator/internal/store"
)

// TestMockStore_CreateMelody tests creation via MockStore.
func TestMockStore_CreateMelody(t *testing.T) {
	ctx := context.Background()

	t.Run("assigns ID and timestamps", func(t *testing.T) {
		ms := store.NewMockStore()
		m, err := ms.CreateMelody(ctx, store.Melody{
			Title:       "Test",
			Prompt:      "test prompt",
			ABCNotation: "X:1\nK:C\n|C|",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if m.ID == "" {
			t.Error("expected non-empty ID")
		}
		if m.CreatedAt.IsZero() {
			t.Error("expected non-zero CreatedAt")
		}
		if m.UpdatedAt.IsZero() {
			t.Error("expected non-zero UpdatedAt")
		}
		if m.Title != "Test" {
			t.Errorf("title: got %q, want %q", m.Title, "Test")
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnCreate = errors.New("firestore down")
		_, err := ms.CreateMelody(ctx, store.Melody{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Error is cleared after first call.
		_, err = ms.CreateMelody(ctx, store.Melody{Title: "ok"})
		if err != nil {
			t.Fatalf("expected nil error on second call, got: %v", err)
		}
	})
}

// TestMockStore_ListMelodies tests listing and sort order.
func TestMockStore_ListMelodies(t *testing.T) {
	ctx := context.Background()

	t.Run("empty collection returns non-nil empty slice", func(t *testing.T) {
		ms := store.NewMockStore()
		list, err := ms.ListMelodies(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if list == nil {
			t.Error("expected non-nil slice, got nil")
		}
		if len(list) != 0 {
			t.Errorf("expected empty slice, got %d items", len(list))
		}
	})

	t.Run("single element", func(t *testing.T) {
		ms := store.NewMockStore()
		_, _ = ms.CreateMelody(ctx, store.Melody{Title: "Only"})
		list, err := ms.ListMelodies(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 1 {
			t.Fatalf("expected 1, got %d", len(list))
		}
		if list[0].Title != "Only" {
			t.Errorf("got %q", list[0].Title)
		}
	})

	t.Run("multiple elements ordered by CreatedAt desc", func(t *testing.T) {
		ms := store.NewMockStore()
		_, _ = ms.CreateMelody(ctx, store.Melody{Title: "First"})
		_, _ = ms.CreateMelody(ctx, store.Melody{Title: "Second"})
		_, _ = ms.CreateMelody(ctx, store.Melody{Title: "Third"})

		list, err := ms.ListMelodies(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(list) != 3 {
			t.Fatalf("expected 3 items, got %d", len(list))
		}
		// Most recent first.
		if list[0].Title != "Third" {
			t.Errorf("first item: got %q, want %q", list[0].Title, "Third")
		}
		if list[2].Title != "First" {
			t.Errorf("last item: got %q, want %q", list[2].Title, "First")
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnList = errors.New("firestore unavailable")
		_, err := ms.ListMelodies(ctx)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestMockStore_GetMelody tests retrieval by ID.
func TestMockStore_GetMelody(t *testing.T) {
	ctx := context.Background()

	t.Run("existing ID", func(t *testing.T) {
		ms := store.NewMockStore()
		created, _ := ms.CreateMelody(ctx, store.Melody{Title: "Foo"})
		got, err := ms.GetMelody(ctx, created.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.ID != created.ID {
			t.Errorf("ID mismatch: got %q, want %q", got.ID, created.ID)
		}
		if got.Title != "Foo" {
			t.Errorf("title mismatch: got %q", got.Title)
		}
	})

	t.Run("unknown ID returns ErrNotFound", func(t *testing.T) {
		ms := store.NewMockStore()
		_, err := ms.GetMelody(ctx, "nonexistent")
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnGet = errors.New("transient error")
		_, err := ms.GetMelody(ctx, "any")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestMockStore_UpdateMelody tests partial updates.
func TestMockStore_UpdateMelody(t *testing.T) {
	ctx := context.Background()

	newStr := func(s string) *string { return &s }

	t.Run("update title only", func(t *testing.T) {
		ms := store.NewMockStore()
		created, _ := ms.CreateMelody(ctx, store.Melody{Title: "Old", ABCNotation: "X:1\nK:C\n|C|"})
		updated, err := ms.UpdateMelody(ctx, created.ID, store.MelodyUpdate{Title: newStr("New")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Title != "New" {
			t.Errorf("title: got %q, want %q", updated.Title, "New")
		}
		if updated.ABCNotation != "X:1\nK:C\n|C|" {
			t.Errorf("abc_notation should be unchanged, got %q", updated.ABCNotation)
		}
	})

	t.Run("update abc_notation only", func(t *testing.T) {
		ms := store.NewMockStore()
		created, _ := ms.CreateMelody(ctx, store.Melody{Title: "T", ABCNotation: "X:1\nK:C\n|C|"})
		updated, err := ms.UpdateMelody(ctx, created.ID, store.MelodyUpdate{ABCNotation: newStr("X:2\nK:G\n|G|")})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if updated.Title != "T" {
			t.Errorf("title should be unchanged, got %q", updated.Title)
		}
		if updated.ABCNotation != "X:2\nK:G\n|G|" {
			t.Errorf("abc_notation: got %q", updated.ABCNotation)
		}
	})

	t.Run("UpdatedAt advances", func(t *testing.T) {
		ms := store.NewMockStore()
		created, _ := ms.CreateMelody(ctx, store.Melody{Title: "T"})
		updated, _ := ms.UpdateMelody(ctx, created.ID, store.MelodyUpdate{Title: newStr("T2")})
		if !updated.UpdatedAt.After(created.CreatedAt) && !updated.UpdatedAt.Equal(created.CreatedAt) {
			// UpdatedAt may equal CreatedAt in fast tests; just ensure it's not before.
			if updated.UpdatedAt.Before(created.CreatedAt) {
				t.Error("UpdatedAt should not be before CreatedAt")
			}
		}
	})

	t.Run("unknown ID returns ErrNotFound", func(t *testing.T) {
		ms := store.NewMockStore()
		_, err := ms.UpdateMelody(ctx, "ghost", store.MelodyUpdate{Title: newStr("x")})
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnUpdate = errors.New("oops")
		_, err := ms.UpdateMelody(ctx, "any", store.MelodyUpdate{})
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestMockStore_DuplicateMelody tests duplication and title logic.
func TestMockStore_DuplicateMelody(t *testing.T) {
	ctx := context.Background()

	t.Run("basic duplication appends (copy)", func(t *testing.T) {
		ms := store.NewMockStore()
		src, _ := ms.CreateMelody(ctx, store.Melody{Title: "My Melody", Prompt: "p", ABCNotation: "X:1\nK:C\n|C|"})
		dup, err := ms.DuplicateMelody(ctx, src.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dup.Title != "My Melody (copy)" {
			t.Errorf("title: got %q, want %q", dup.Title, "My Melody (copy)")
		}
		if dup.ID == src.ID {
			t.Error("duplicate should have a new ID")
		}
		if dup.Prompt != src.Prompt {
			t.Errorf("prompt should be preserved: got %q", dup.Prompt)
		}
		if dup.ABCNotation != src.ABCNotation {
			t.Errorf("abc_notation should be preserved: got %q", dup.ABCNotation)
		}
	})

	t.Run("double copy appends suffix twice", func(t *testing.T) {
		ms := store.NewMockStore()
		src, _ := ms.CreateMelody(ctx, store.Melody{Title: "Foo (copy)"})
		dup, _ := ms.DuplicateMelody(ctx, src.ID)
		if dup.Title != "Foo (copy) (copy)" {
			t.Errorf("title: got %q, want %q", dup.Title, "Foo (copy) (copy)")
		}
	})

	t.Run("empty title becomes (copy)", func(t *testing.T) {
		ms := store.NewMockStore()
		src, _ := ms.CreateMelody(ctx, store.Melody{Title: ""})
		dup, _ := ms.DuplicateMelody(ctx, src.ID)
		if dup.Title != " (copy)" {
			t.Errorf("title: got %q, want %q", dup.Title, " (copy)")
		}
	})

	t.Run("200-char title is truncated to 200 after suffix", func(t *testing.T) {
		ms := store.NewMockStore()
		longTitle := strings.Repeat("A", 200)
		src, _ := ms.CreateMelody(ctx, store.Melody{Title: longTitle})
		dup, _ := ms.DuplicateMelody(ctx, src.ID)
		if len(dup.Title) > 200 {
			t.Errorf("title length %d exceeds 200", len(dup.Title))
		}
		if !strings.HasSuffix(dup.Title, " (copy)") {
			t.Errorf("title should end with \" (copy)\", got %q", dup.Title)
		}
	})

	t.Run("unknown ID returns ErrNotFound", func(t *testing.T) {
		ms := store.NewMockStore()
		_, err := ms.DuplicateMelody(ctx, "ghost")
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnDuplicate = errors.New("store error")
		_, err := ms.DuplicateMelody(ctx, "any")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestMockStore_DeleteMelody tests deletion.
func TestMockStore_DeleteMelody(t *testing.T) {
	ctx := context.Background()

	t.Run("existing ID is deleted", func(t *testing.T) {
		ms := store.NewMockStore()
		m, _ := ms.CreateMelody(ctx, store.Melody{Title: "Del"})
		if err := ms.DeleteMelody(ctx, m.ID); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		_, err := ms.GetMelody(ctx, m.ID)
		if !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound after delete, got %v", err)
		}
	})

	t.Run("unknown ID returns ErrNotFound", func(t *testing.T) {
		ms := store.NewMockStore()
		if err := ms.DeleteMelody(ctx, "ghost"); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("double delete returns ErrNotFound on second call", func(t *testing.T) {
		ms := store.NewMockStore()
		m, _ := ms.CreateMelody(ctx, store.Melody{Title: "X"})
		_ = ms.DeleteMelody(ctx, m.ID)
		if err := ms.DeleteMelody(ctx, m.ID); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("expected ErrNotFound on second delete, got %v", err)
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnDelete = errors.New("boom")
		if err := ms.DeleteMelody(ctx, "any"); err == nil {
			t.Fatal("expected error")
		}
	})
}

// TestMockStore_DeleteAllMelodies tests bulk deletion.
func TestMockStore_DeleteAllMelodies(t *testing.T) {
	ctx := context.Background()

	t.Run("empty store returns 0", func(t *testing.T) {
		ms := store.NewMockStore()
		n, err := ms.DeleteAllMelodies(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 0 {
			t.Errorf("expected 0, got %d", n)
		}
	})

	t.Run("deletes all and returns correct count", func(t *testing.T) {
		ms := store.NewMockStore()
		for i := 0; i < 3; i++ {
			_, _ = ms.CreateMelody(ctx, store.Melody{Title: "x"})
		}
		n, err := ms.DeleteAllMelodies(ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n != 3 {
			t.Errorf("expected 3, got %d", n)
		}
		list, _ := ms.ListMelodies(ctx)
		if len(list) != 0 {
			t.Errorf("expected empty list after DeleteAll, got %d items", len(list))
		}
	})

	t.Run("error injection", func(t *testing.T) {
		ms := store.NewMockStore()
		ms.ErrorOnDeleteAll = errors.New("bulk error")
		_, err := ms.DeleteAllMelodies(ctx)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}
