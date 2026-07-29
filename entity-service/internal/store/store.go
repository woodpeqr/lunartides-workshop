// Package store is the file-backed state of entity-service.
//
// State lives in one JSON file on disk (path from env ENTITY_STORE_PATH,
// default /data/entities.json), holding a single object:
//
//	{"entities": {"ent_x": {..}, "ent_y": {..}, ...}}
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

// ErrNotFound is returned when an entity id is unknown. Handlers map it to 404.
var ErrNotFound = errors.New("not found")

// DefaultStorePath is used when ENTITY_STORE_PATH is unset.
const DefaultStorePath = "/data/entities.json"

// Entity is the wire shape returned to clients.
type Entity struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes"`
	Version    int               `json:"version"`
	CreatedAt  string            `json:"createdAt"`
	UpdatedAt  string            `json:"updatedAt"`
}

// Input is the mutable subset a client supplies on create/update.
type Input struct {
	Name       string            `json:"name"`
	Type       string            `json:"type"`
	Status     string            `json:"status"`
	Attributes map[string]string `json:"attributes"`
}

// storeFile is the on-disk JSON envelope.
type storeFile struct {
	Entities map[string]Entity `json:"entities"`
}

// Store is the file-backed entity store.
type Store struct {
	path string
}

// New returns a Store backed by path.
func New(path string) *Store {
	if path == "" {
		path = DefaultStorePath
	}
	return &Store{path: path}
}

// Path returns the on-disk store path (used at startup to ensure its directory
// exists).
func (s *Store) Path() string { return s.path }

// load reads and decodes the store file.
func (s *Store) load(ctx context.Context) (map[string]Entity, error) {
	raw, err := os.ReadFile(s.path)
	if err != nil {
		// A missing file is the valid empty starting state, not an error.
		if errors.Is(err, os.ErrNotExist) {
			return map[string]Entity{}, nil
		}
		return nil, err
	}

	var sf storeFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return nil, err
	}
	if sf.Entities == nil {
		sf.Entities = map[string]Entity{}
	}
	return sf.Entities, nil
}

// save encodes and writes the store file.
func (s *Store) save(ctx context.Context, entities map[string]Entity) error {
	raw, err := json.Marshal(storeFile{Entities: entities})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, raw, 0o644)
}

// newID returns a fresh entity id. crypto/rand (not a time seed) keeps ids
// unique even when created in rapid succession.
func newID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "ent_" + hex.EncodeToString(b[:]), nil
}

// Create assigns an id, sets version 1 and timestamps, and stores the entity.
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

// Get returns one entity. ErrNotFound if unknown.
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

// Update fully replaces the mutable fields, increments version, and bumps
// updatedAt. ErrNotFound if missing.
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

// Delete removes an entity. ErrNotFound if missing.
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

// List returns every entity, sorted by id for stable output.
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

// Wipe resets the store to empty.
func (s *Store) Wipe(ctx context.Context) error {
	return s.save(ctx, map[string]Entity{})
}
