package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// ctx is the background context threaded into store calls; the store opens
// child spans off it (no-op under the test's default global tracer).
var ctx = context.Background()

// newStore returns a Store backed by a fresh temp-dir file so tests never touch
// the real /data path and each test is isolated.
func newStore(t *testing.T) *Store {
	t.Helper()
	return New(filepath.Join(t.TempDir(), "entities.json"))
}

func TestCreateAssignsIDVersionTimestamps(t *testing.T) {
	s := newStore(t)
	e, err := s.Create(ctx, Input{Name: "rack1-sw3", Type: "switch", Status: "active",
		Attributes: map[string]string{"site": "dc1"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.ID == "" || len(e.ID) < 5 || e.ID[:4] != "ent_" {
		t.Fatalf("expected ent_ prefixed id, got %q", e.ID)
	}
	if e.Version != 1 {
		t.Fatalf("expected version 1, got %d", e.Version)
	}
	if e.CreatedAt == "" || e.UpdatedAt == "" {
		t.Fatalf("expected timestamps, got created=%q updated=%q", e.CreatedAt, e.UpdatedAt)
	}
	if e.Name != "rack1-sw3" || e.Type != "switch" || e.Status != "active" {
		t.Fatalf("field mismatch: %+v", e)
	}
	if e.Attributes["site"] != "dc1" {
		t.Fatalf("attributes not preserved: %+v", e.Attributes)
	}
}

func TestCreateGetRoundTrip(t *testing.T) {
	s := newStore(t)
	created, _ := s.Create(ctx, Input{Name: "n", Type: "router", Status: "active"})
	got, err := s.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != created.ID || got.Type != "router" || got.Version != 1 {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

func TestGetNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Get(ctx, "ent_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateReplacesAndIncrementsVersion(t *testing.T) {
	s := newStore(t)
	created, _ := s.Create(ctx, Input{Name: "old", Type: "switch", Status: "active",
		Attributes: map[string]string{"a": "1"}})

	updated, err := s.Update(ctx, created.ID, Input{Name: "new", Type: "firewall",
		Status: "maintenance", Attributes: map[string]string{"b": "2"}})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("expected version 2, got %d", updated.Version)
	}
	if updated.Name != "new" || updated.Type != "firewall" || updated.Status != "maintenance" {
		t.Fatalf("update did not replace fields: %+v", updated)
	}
	if _, ok := updated.Attributes["a"]; ok {
		t.Fatalf("update should fully replace attributes, still has old key: %+v", updated.Attributes)
	}
	if updated.Attributes["b"] != "2" {
		t.Fatalf("update did not set new attributes: %+v", updated.Attributes)
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Fatalf("createdAt must not change on update: %q != %q", updated.CreatedAt, created.CreatedAt)
	}

	// Persisted version reflects the update.
	got, _ := s.Get(ctx, created.ID)
	if got.Version != 2 || got.Name != "new" {
		t.Fatalf("persisted update mismatch: %+v", got)
	}
}

func TestUpdateNotFound(t *testing.T) {
	s := newStore(t)
	if _, err := s.Update(ctx, "ent_missing", Input{Name: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteRemoves(t *testing.T) {
	s := newStore(t)
	created, _ := s.Create(ctx, Input{Name: "n", Type: "ap", Status: "active"})
	if err := s.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected entity gone after delete, got %v", err)
	}
}

func TestDeleteNotFound(t *testing.T) {
	s := newStore(t)
	if err := s.Delete(ctx, "ent_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListReturnsAllSorted(t *testing.T) {
	s := newStore(t)
	e1, _ := s.Create(ctx, Input{Name: "a", Type: "server", Status: "active"})
	e2, _ := s.Create(ctx, Input{Name: "b", Type: "router", Status: "active"})
	e3, _ := s.Create(ctx, Input{Name: "c", Type: "switch", Status: "active"})

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("expected 3 entities, got %d", len(list))
	}
	// Sorted by id ascending.
	for i := 1; i < len(list); i++ {
		if list[i-1].ID > list[i].ID {
			t.Fatalf("list not sorted by id: %v", list)
		}
	}
	// All ids present.
	seen := map[string]bool{}
	for _, e := range list {
		seen[e.ID] = true
	}
	for _, id := range []string{e1.ID, e2.ID, e3.ID} {
		if !seen[id] {
			t.Fatalf("id %q missing from list", id)
		}
	}
}

func TestListEmpty(t *testing.T) {
	s := newStore(t)
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List on empty store: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

func TestWipeResetsState(t *testing.T) {
	s := newStore(t)
	created, _ := s.Create(ctx, Input{Name: "gone", Type: "switch", Status: "active"})

	if err := s.Wipe(ctx); err != nil {
		t.Fatalf("Wipe: %v", err)
	}
	if _, err := s.Get(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("wipe should clear entities")
	}
	list, _ := s.List(ctx)
	if len(list) != 0 {
		t.Fatalf("expected empty after wipe, got %d", len(list))
	}

	// Fresh writes work after wipe.
	if _, err := s.Create(ctx, Input{Name: "again", Type: "ap", Status: "active"}); err != nil {
		t.Fatalf("Create after wipe: %v", err)
	}
}

func TestCreateUniqueIDs(t *testing.T) {
	s := newStore(t)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		e, err := s.Create(ctx, Input{Name: "n", Type: "server", Status: "active"})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		if seen[e.ID] {
			t.Fatalf("duplicate id generated: %q", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestLoadInvalidFileReturnsError(t *testing.T) {
	// A partial/invalid JSON file makes reads return an error rather than
	// silently serving empty.
	s := newStore(t)
	if err := os.WriteFile(s.Path(), []byte(`{"entities": {"ent_x": {`), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("expected parse error, got nil")
	}
	if _, err := s.Get(ctx, "ent_x"); err == nil {
		t.Fatal("expected parse error for Get, got nil")
	}
}

func TestMissingFileIsEmptyStore(t *testing.T) {
	// First run: no file yet. Reads must succeed and report an empty store, not
	// a 500.
	s := newStore(t)
	if _, err := os.Stat(s.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no file yet, stat err=%v", err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List on missing file should succeed: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list on missing file, got %d", len(list))
	}
	if _, err := s.Get(ctx, "ent_x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on missing file Get, got %v", err)
	}
}
