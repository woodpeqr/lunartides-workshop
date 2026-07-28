// Package store is the file-backed state of entity-service.
//
// State lives in ONE JSON file on disk (path from env ENTITY_STORE_PATH,
// default /data/entities.json). The file holds a single JSON object:
//
//	{"entities": {"ent_x": {..}, "ent_y": {..}, ...}}
//
// DESIGN — NO SAFEGUARDS (this is the fragility engine): the service is
// functionally correct for normal single-threaded use, but deliberately has no
// protections, so it degrades/corrupts/OOMs under load. Telemetry is how
// students observe the failures.
//
//   - NO in-memory cache. Every read opens the file, json.Unmarshals the WHOLE
//     file, and serves from that. Every write reads+unmarshals the whole file,
//     mutates, then json.Marshals the whole file and writes it back.
//   - Writes are NON-ATOMIC (os.WriteFile straight onto the target path — no
//     temp-file-then-rename). NO file locking, NO mutex serializing file access
//     across requests. Concurrent writes can interleave and tear the file.
//   - NO cap on entity count or attribute/payload size. Unbounded.
//
// These absences are the POINT — do not "fix" them. The three failure modes:
//   - F1: O(n) full-file marshal/unmarshal per op → latency + memory grow.
//   - F2: unbounded growth → huge file → unmarshal allocates it all → OOM.
//   - F3: concurrent non-atomic writes → torn file → subsequent Unmarshal FAILS
//     → 500.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"time"
)

// ErrNotFound is returned when an entity id is unknown. Handlers map this to
// HTTP 404 (error shape {"error":"not found"}).
var ErrNotFound = errors.New("not found")

// DefaultStorePath is the on-disk store location when ENTITY_STORE_PATH is
// unset.
const DefaultStorePath = "/data/entities.json"

// Entity is the frozen wire shape (SolarWinds-style; an entity is anything,
// concrete examples are network devices).
type Entity struct {
	ID         string            `json:"id"`         // server-assigned: "ent_"+hex
	Name       string            `json:"name"`       // free string (device hostname in practice)
	Type       string            `json:"type"`       // free string: server|router|switch|workstation|firewall|ap
	Status     string            `json:"status"`     // free string: active|offline|maintenance|decommissioned
	Attributes map[string]string `json:"attributes"` // free-form map — arbitrary size (flood payload knob)
	Version    int               `json:"version"`    // starts at 1, increments on each update
	CreatedAt  string            `json:"createdAt"`  // RFC3339
	UpdatedAt  string            `json:"updatedAt"`  // RFC3339
}

// Input is the mutable subset a client supplies on create/update (full replace
// of these fields).
type Input struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes"`
}

// storeFile is the on-disk envelope: a single JSON object keyed by entity id.
type storeFile struct {
	Entities map[string]Entity `json:"entities"`
}

// Store is the file-backed entity store. It holds NO entity state in memory —
// only the file path.
type Store struct {
	// path is the single JSON file that is the source of truth.
	path string
}

// New returns a Store backed by path.
func New(path string) *Store {
	if path == "" {
		path = DefaultStorePath
	}
	return &Store{path: path}
}

// Path returns the on-disk store path (used by startup plumbing to ensure the
// parent directory exists).
func (s *Store) Path() string { return s.path }

// load opens the store file, reads it whole, and json.Unmarshals it. This is
// the O(n) read path (F1) and the OOM allocation site (F2). A missing file is
// treated as an empty store (first-run), NOT an error. A parse failure (F3 —
// torn/partial file from a concurrent non-atomic write) returns the error.
func (s *Store) load(ctx context.Context) (map[string]Entity, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First run: no file yet. An empty store is the correct starting state.
			return map[string]Entity{}, nil
		}
		return nil, err
	}

	var sf storeFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		// F3: the file is torn/partial (concurrent non-atomic writes interleaved).
		return nil, err
	}
	if sf.Entities == nil {
		sf.Entities = map[string]Entity{}
	}
	return sf.Entities, nil
}

// save json.Marshals the WHOLE entity map and writes it back to the file
// NON-ATOMICALLY (os.WriteFile straight onto the target path — no
// temp-file-then-rename, no locking). This is the O(n) write path (F1) and the
// tear point (F3): two concurrent savers can interleave their writes and leave
// the file half-written.
func (s *Store) save(ctx context.Context, entities map[string]Entity) error {
	raw, err := json.Marshal(storeFile{Entities: entities})
	if err != nil {
		return err
	}

	// Non-atomic write: no temp+rename, no flock. Deliberate.
	return os.WriteFile(s.path, raw, 0o644)
}

// newID returns a fresh entity id: "ent_" + hex of 8 crypto/rand bytes. Uses
// crypto/rand (NOT time-based randomness) so a tight create loop cannot collide.
func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ent_" + hex.EncodeToString(b[:]), nil
}

// Create assigns an id, sets version=1 and timestamps, then reads+mutates+writes
// the whole file.
func (s *Store) Create(ctx context.Context, in Input) (Entity, error) {
	entities, err := s.load(ctx)
	if err != nil {
		return Entity{}, err
	}

	id, err := newID()
	if err != nil {
		return Entity{}, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	e := Entity{
		ID:         id,
		Name:       in.Name,
		Type:       in.Type,
		Status:     in.Status,
		Attributes: in.Attributes,
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	entities[id] = e

	if err := s.save(ctx, entities); err != nil {
		return Entity{}, err
	}
	return e, nil
}

// Get reads the whole file and serves one entity. ErrNotFound if unknown.
func (s *Store) Get(ctx context.Context, id string) (Entity, error) {
	entities, err := s.load(ctx)
	if err != nil {
		return Entity{}, err
	}

	e, ok := entities[id]
	if !ok {
		return Entity{}, ErrNotFound
	}
	return e, nil
}

// Update fully replaces name/type/status/attributes, increments version, and
// bumps updatedAt. 404 if missing. Reads+mutates+writes the whole file.
func (s *Store) Update(ctx context.Context, id string, in Input) (Entity, error) {
	entities, err := s.load(ctx)
	if err != nil {
		return Entity{}, err
	}

	e, ok := entities[id]
	if !ok {
		return Entity{}, ErrNotFound
	}

	e.Name = in.Name
	e.Type = in.Type
	e.Status = in.Status
	e.Attributes = in.Attributes
	e.Version++
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	entities[id] = e

	if err := s.save(ctx, entities); err != nil {
		return Entity{}, err
	}
	return e, nil
}

// Delete removes an entity. ErrNotFound if missing. Reads+mutates+writes whole.
func (s *Store) Delete(ctx context.Context, id string) error {
	entities, err := s.load(ctx)
	if err != nil {
		return err
	}

	if _, ok := entities[id]; !ok {
		return ErrNotFound
	}
	delete(entities, id)

	return s.save(ctx, entities)
}

// List returns every entity. THIS is the O(n) slow path: it unmarshals the
// whole file and marshals every entity. NO pagination, NO cap. Sorted by id for
// stable output.
func (s *Store) List(ctx context.Context) ([]Entity, error) {
	entities, err := s.load(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]Entity, 0, len(entities))
	for _, e := range entities {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	return out, nil
}

// Wipe resets the store to empty by writing {"entities":{}}. TRUSTED: must be
// correct.
func (s *Store) Wipe(ctx context.Context) error {
	return s.save(ctx, map[string]Entity{})
}
