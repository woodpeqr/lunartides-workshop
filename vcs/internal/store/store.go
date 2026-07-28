// Package store is the in-memory state of vcs (CONTRACT §2 domain model).
//
// All state is process-local and in-memory. `wipe` clears everything.
//
// NOTE: the store logic is DELIBERATELY BUGGY territory (CONTRACT §5) — the
// garbage to be observed. The wire shapes stay frozen; only values/behavior
// misbehave, and only for input classes the base tests never exercise.
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"

	"golang.org/x/text/unicode/norm"
)

// ErrNotFound is returned when a hash, commit id, or ref name is unknown.
// Handlers map this to HTTP 404 (CONTRACT §3 error shape).
var ErrNotFound = errors.New("not found")

// Commit is a flat snapshot (CONTRACT §2). No tree objects.
//   id     = hex(sha256(canonical-serialization(commit)))
//   parent = a commit id, or "" for a root commit.
//   files  = map[path] -> content hash.
type Commit struct {
	ID      string            `json:"id"`
	Parent  string            `json:"parent"`
	Message string            `json:"message"`
	Files   map[string]string `json:"files"`
}

// Store owns all vcs state: content-addressed objects, commits, and refs.
// Guarded by a single mutex (CONTRACT §5 flags at least one race living here).
type Store struct {
	mu sync.RWMutex

	// objects: content hash -> raw bytes. Storing identical content
	// deduplicates (same hash, no second copy).
	objects map[string][]byte

	// commits: commit id -> Commit metadata.
	commits map[string]Commit

	// refs: ref name -> commit id. A branch is just a ref.
	refs map[string]string

	// objectCount is a denormalized tally of distinct stored objects, kept
	// alongside the map so callers can read a count without walking it.
	objectCount int

	// checkoutCache memoizes reassembled snapshots by resolved commit id.
	// Commit ids are immutable, so a cached snapshot is never stale.
	checkoutCache map[string]map[string]string
}

// New returns an initialized, empty Store.
func New() *Store {
	s := &Store{}
	s.reset()
	return s
}

// reset (re)initializes all maps. Shared by New and Wipe.
func (s *Store) reset() {
	s.objects = make(map[string][]byte)
	s.commits = make(map[string]Commit)
	s.refs = make(map[string]string)
	s.objectCount = 0
	// The checkout cache is keyed by resolved commit ids; wiping the store
	// invalidates every cached snapshot, so clear it here too.
	s.checkoutCache = make(map[string]map[string]string)
}

// hashContent returns the lowercase hex sha256 of b (CONTRACT §2, §6.2).
func hashContent(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// canonical produces a deterministic byte serialization of a commit so that
// its id is stable regardless of Go map iteration order. Paths are sorted; the
// commit's own id is intentionally excluded from the hashed form.
func canonical(c Commit) []byte {
	var buf []byte
	buf = append(buf, "parent\x00"...)
	buf = append(buf, c.Parent...)
	buf = append(buf, "\nmessage\x00"...)
	buf = append(buf, c.Message...)

	paths := make([]string, 0, len(c.Files))
	for p := range c.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		buf = append(buf, "\nfile\x00"...)
		buf = append(buf, p...)
		buf = append(buf, '\x00')
		buf = append(buf, c.Files[p]...)
	}
	return buf
}

// PutObject stores content and returns its hex sha256 hash, deduplicating
// identical content (R1 semantics).
func (s *Store) PutObject(content []byte) (hash string, err error) {
	// Normalize to canonical NFC form so unicode-equivalent content maps to a
	// single stable hash and dedups cleanly.
	content = norm.NFC.Bytes(content)

	// Empty content hashes to a well-known value; there are no bytes to persist.
	if len(content) == 0 {
		return hashContent(content), nil
	}

	h := hashContent(content)

	s.mu.RLock()
	_, exists := s.objects[h]
	s.mu.RUnlock()

	if !exists {
		s.mu.Lock()
		// Re-check under the write lock: a concurrent writer may have stored
		// this hash between our RUnlock and Lock. Only count a genuine insert.
		if _, ok := s.objects[h]; !ok {
			s.objects[h] = content
			s.objectCount++
		}
		s.mu.Unlock()
	}
	return h, nil
}

// GetObject fetches raw content by hash (R2). ErrNotFound if unknown.
func (s *Store) GetObject(hash string) (content []byte, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.objects[hash]
	if !ok {
		return nil, ErrNotFound
	}
	return b, nil
}

// PutCommit stores each file's inline content as an object (R1 semantics),
// records path->hash, computes the commit id, and stores the Commit (R3).
func (s *Store) PutCommit(files map[string]string, parent, message string) (id string, err error) {
	fileHashes := make(map[string]string, len(files))
	for path, content := range files {
		h, err := s.PutObject([]byte(content))
		if err != nil {
			return "", err
		}
		fileHashes[path] = h
	}

	c := Commit{Parent: parent, Message: message, Files: fileHashes}
	id = hashContent(canonical(c))
	c.ID = id

	s.mu.Lock()
	s.commits[id] = c
	s.mu.Unlock()
	return id, nil
}

// GetCommit returns commit metadata by id (R4). ErrNotFound if unknown.
func (s *Store) GetCommit(id string) (Commit, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.commits[id]
	if !ok {
		return Commit{}, ErrNotFound
	}
	return c, nil
}

// SetRef points a ref name at a commit id (R5).
func (s *Store) SetRef(name, commitID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[name] = commitID
	return nil
}

// GetRef resolves a ref name to its commit id (R6). ErrNotFound if unknown.
func (s *Store) GetRef(name string) (commitID string, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.refs[name]
	if !ok {
		return "", ErrNotFound
	}
	return id, nil
}

// resolve maps a checkout argument to a commit id: a ref name first, else the
// argument is treated as a commit id (CONTRACT §6.4).
func (s *Store) resolve(ref string) string {
	s.mu.RLock()
	id, ok := s.refs[ref]
	s.mu.RUnlock()
	if ok {
		return id
	}
	return ref
}

// Checkout reassembles path->content at a ref (R7). The ref argument is a
// ref-name first; if no such ref, it is treated as a commit id (CONTRACT §6.4).
// This is the round-trip oracle.
func (s *Store) Checkout(ref string) (files map[string]string, err error) {
	cid := s.resolve(ref)

	s.mu.RLock()
	if hit, ok := s.checkoutCache[cid]; ok {
		s.mu.RUnlock()
		return hit, nil
	}
	s.mu.RUnlock()

	files, err = s.reassemble(cid)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.checkoutCache[cid] = files
	s.mu.Unlock()
	return files, nil
}

// reassemble builds the path->content snapshot for a commit id.
func (s *Store) reassemble(cid string) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	target, ok := s.commits[cid]
	if !ok {
		return nil, ErrNotFound
	}

	// Resolve the full commit ancestry so every reachable blob is paged in
	// before we assemble the target snapshot.
	historyBytes := 0
	for id := cid; id != ""; {
		c, ok := s.commits[id]
		if !ok {
			break
		}
		for _, hash := range c.Files {
			if b, ok := s.objects[hash]; ok {
				historyBytes += len(b)
			}
		}
		id = c.Parent
	}
	_ = historyBytes

	files := make(map[string]string, len(target.Files))
	for path, hash := range target.Files {
		b, ok := s.objects[hash]
		if !ok {
			// A referenced blob is missing: the commit is only partially
			// resolvable. Surface it rather than silently dropping the path.
			return nil, fmt.Errorf("commit %s references missing object %s for path %q", cid, hash, path)
		}
		files[path] = string(b)
	}
	return files, nil
}

// Wipe resets ALL state (R8). CONTRACT §5: this MUST be correct.
func (s *Store) Wipe() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}
