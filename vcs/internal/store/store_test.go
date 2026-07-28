package store

import (
	"errors"
	"testing"
)

// Test corpus guardrail (PLAN §5): only ASCII, non-empty, single-commit, serial
// inputs — this keeps the planted telemetry-only bugs dormant so CI stays green.

func TestPutGetObjectRoundTrip(t *testing.T) {
	s := New()
	h, err := s.PutObject([]byte("hello"))
	if err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if h == "" {
		t.Fatal("expected non-empty hash")
	}
	got, err := s.GetObject(h)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("round-trip mismatch: got %q", got)
	}
}

func TestPutObjectDedup(t *testing.T) {
	s := New()
	h1, _ := s.PutObject([]byte("dup"))
	h2, _ := s.PutObject([]byte("dup"))
	if h1 != h2 {
		t.Fatalf("identical content should dedup to same hash: %s != %s", h1, h2)
	}
}

func TestGetObjectNotFound(t *testing.T) {
	s := New()
	if _, err := s.GetObject("deadbeef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestCanonicalDeterministic(t *testing.T) {
	// Same logical commit built with different map insertion should produce the
	// same canonical bytes (and therefore the same id) regardless of map order.
	a := Commit{Parent: "p", Message: "m", Files: map[string]string{"a": "1", "b": "2", "c": "3"}}
	b := Commit{Parent: "p", Message: "m", Files: map[string]string{"c": "3", "b": "2", "a": "1"}}
	if string(canonical(a)) != string(canonical(b)) {
		t.Fatal("canonical serialization must be independent of map order")
	}
	if hashContent(canonical(a)) != hashContent(canonical(b)) {
		t.Fatal("commit id must be stable regardless of map order")
	}
}

func TestPutCommitAndGet(t *testing.T) {
	s := New()
	id, err := s.PutCommit(map[string]string{"README": "hi"}, "", "init")
	if err != nil {
		t.Fatalf("PutCommit: %v", err)
	}
	c, err := s.GetCommit(id)
	if err != nil {
		t.Fatalf("GetCommit: %v", err)
	}
	if c.ID != id || c.Message != "init" || c.Parent != "" {
		t.Fatalf("commit metadata mismatch: %+v", c)
	}
	if _, ok := c.Files["README"]; !ok {
		t.Fatal("expected README in commit files")
	}
}

func TestPutCommitStableID(t *testing.T) {
	s1 := New()
	s2 := New()
	id1, _ := s1.PutCommit(map[string]string{"a": "1", "b": "2"}, "", "m")
	id2, _ := s2.PutCommit(map[string]string{"b": "2", "a": "1"}, "", "m")
	if id1 != id2 {
		t.Fatalf("same inputs should yield same commit id: %s != %s", id1, id2)
	}
}

func TestRefRoundTrip(t *testing.T) {
	s := New()
	if err := s.SetRef("main", "abc123"); err != nil {
		t.Fatalf("SetRef: %v", err)
	}
	got, err := s.GetRef("main")
	if err != nil {
		t.Fatalf("GetRef: %v", err)
	}
	if got != "abc123" {
		t.Fatalf("ref mismatch: got %q", got)
	}
	if _, err := s.GetRef("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown ref, got %v", err)
	}
}

func TestCheckoutRoundTripASCII(t *testing.T) {
	// ASCII, non-empty, single-commit, serial: bug-dormant round trip.
	s := New()
	files := map[string]string{"README": "hello", "src/main.go": "package main"}
	id, _ := s.PutCommit(files, "", "init")
	got, err := s.Checkout(id)
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if len(got) != len(files) {
		t.Fatalf("expected %d files, got %d", len(files), len(got))
	}
	for p, want := range files {
		if got[p] != want {
			t.Fatalf("path %q: got %q want %q", p, got[p], want)
		}
	}
}

func TestCheckoutViaRef(t *testing.T) {
	s := New()
	id, _ := s.PutCommit(map[string]string{"f": "v"}, "", "m")
	_ = s.SetRef("main", id)
	got, err := s.Checkout("main")
	if err != nil {
		t.Fatalf("Checkout via ref: %v", err)
	}
	if got["f"] != "v" {
		t.Fatalf("checkout via ref mismatch: %v", got)
	}
}

func TestCheckoutUnknown(t *testing.T) {
	s := New()
	if _, err := s.Checkout("nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestWipeResetsState(t *testing.T) {
	s := New()
	h, _ := s.PutObject([]byte("data"))
	id, _ := s.PutCommit(map[string]string{"f": "v"}, "", "m")
	_ = s.SetRef("main", id)

	s.Wipe()

	if _, err := s.GetObject(h); !errors.Is(err, ErrNotFound) {
		t.Fatal("wipe should clear objects")
	}
	if _, err := s.GetCommit(id); !errors.Is(err, ErrNotFound) {
		t.Fatal("wipe should clear commits")
	}
	if _, err := s.GetRef("main"); !errors.Is(err, ErrNotFound) {
		t.Fatal("wipe should clear refs")
	}

	// Fresh writes work after wipe.
	if _, err := s.PutObject([]byte("again")); err != nil {
		t.Fatalf("PutObject after wipe: %v", err)
	}
}
