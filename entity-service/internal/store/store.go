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
//     → 500 + an ERROR log naming the parse failure.
//
// Telemetry (the workshop): every store method opens a child span under the
// caller's request span, records errors, and feeds the store metrics
// (count/file-bytes gauges + per-op latency histogram + error counter +
// parse-error counter). file_bytes is the OOM signal — entity flood shows up as
// unbounded file growth and resident heap during unmarshal.
package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/telemetry"
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

// Store is the file-backed entity store with its OTel instruments installed.
// It holds NO entity state in memory — only the file path, instruments, and a
// denormalized count for the observable gauge.
type Store struct {
	// path is the single JSON file that is the source of truth.
	path string

	// count is a denormalized tally of entities, refreshed after each successful
	// save so the entity.store.count gauge reflects reality without re-reading
	// the file on every collection. Atomic because collection callbacks run
	// concurrently with write ops (this guards ONLY the counter — never the file
	// access, which is deliberately unguarded).
	count atomic.Int64

	tracer  trace.Tracer
	metrics storeMetrics
}

// storeMetrics bundles the store instruments. Gauges are observable; the
// histogram/counters are recorded per call.
type storeMetrics struct {
	opDuration  metric.Float64Histogram // entity.store.operation.duration (ms)
	opErrors    metric.Int64Counter     // entity.store.operation.errors
	parseErrors metric.Int64Counter     // entity.store.parse_errors
}

// New returns a Store backed by path, with its OTel instruments installed.
func New(path string) *Store {
	if path == "" {
		path = DefaultStorePath
	}
	s := &Store{
		path:   path,
		tracer: otel.Tracer("entity-service"),
	}
	s.initMetrics()
	return s
}

// Path returns the on-disk store path (used by startup plumbing to ensure the
// parent directory exists).
func (s *Store) Path() string { return s.path }

// initMetrics resolves the store instruments from the global meter and wires
// the observable gauges. Errors here are non-fatal: under the noop provider
// (tests) the meter hands back valid no-op instruments.
func (s *Store) initMetrics() {
	meter := otel.Meter("entity-service")

	s.metrics.opDuration, _ = meter.Float64Histogram(
		"entity.store.operation.duration",
		metric.WithDescription("Latency of entity store operations."),
		metric.WithUnit("ms"),
	)
	s.metrics.opErrors, _ = meter.Int64Counter(
		"entity.store.operation.errors",
		metric.WithDescription("Entity store operations that returned an error."),
	)
	s.metrics.parseErrors, _ = meter.Int64Counter(
		"entity.store.parse_errors",
		metric.WithDescription("Failures to parse (json.Unmarshal) the entity store file — torn/partial file signal."),
	)

	// Observable gauges: sampled on collection.
	//   count      — number of entities, read from the denormalized tally.
	//   file_bytes — current size of the store file on disk (os.Stat). This is
	//                the key resource signal: an entity/payload flood shows up
	//                here as unbounded file growth (and heap during unmarshal).
	count, _ := meter.Int64ObservableGauge(
		"entity.store.count",
		metric.WithDescription("Number of entities in the store (reflects reality after each write)."),
		metric.WithUnit("{entity}"),
	)
	fileBytes, _ := meter.Int64ObservableGauge(
		"entity.store.file_bytes",
		metric.WithDescription("Current size of the entity store file on disk (OOM/growth signal)."),
		metric.WithUnit("By"),
	)

	_, _ = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			o.ObserveInt64(count, s.count.Load())
			var sz int64
			if fi, err := os.Stat(s.path); err == nil {
				sz = fi.Size()
			}
			o.ObserveInt64(fileBytes, sz)
			return nil
		},
		count, fileBytes,
	)
}

// observe records an operation's latency and, on error, bumps the error
// counter. Deferred at the top of each store method with the op name.
func (s *Store) observe(ctx context.Context, op string, start time.Time, errp *error) {
	attrs := metric.WithAttributes(attribute.String("entity.store.op", op))
	s.metrics.opDuration.Record(ctx, float64(time.Since(start).Microseconds())/1000.0, attrs)
	if errp != nil && *errp != nil {
		s.metrics.opErrors.Add(ctx, 1, attrs)
	}
}

// fileSize returns the on-disk size of the store file, or 0 if it cannot be
// stat'd. Used to annotate spans.
func (s *Store) fileSize() int64 {
	if fi, err := os.Stat(s.path); err == nil {
		return fi.Size()
	}
	return 0
}

// load opens the store file, reads it whole, and json.Unmarshals it. This is
// the O(n) read path (F1) and the OOM allocation site (F2). A missing file is
// treated as an empty store (first-run), NOT an error. A parse failure (F3 —
// torn/partial file from a concurrent non-atomic write) increments the
// parse-error counter, emits the key ERROR log, and returns the error.
func (s *Store) load(ctx context.Context) (_ map[string]Entity, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.load")
	defer span.End()
	defer s.observe(ctx, "Load", start, &err)

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First run: no file yet. An empty store is the correct starting state.
			span.SetAttributes(
				attribute.Int("entity.count", 0),
				attribute.Int64("entity.store.file_bytes", 0),
			)
			err = nil
			return map[string]Entity{}, nil
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, "read store file")
		telemetry.Error(ctx, "failed to read entity store file",
			otellog.String("store.path", s.path),
			otellog.String("error", err.Error()),
		)
		return nil, err
	}

	span.SetAttributes(attribute.Int64("entity.store.file_bytes", int64(len(raw))))

	var sf storeFile
	if uerr := json.Unmarshal(raw, &sf); uerr != nil {
		// F3: the file is torn/partial (concurrent non-atomic writes interleaved).
		// This ERROR log is the ONLY thing that reveals the root cause.
		err = uerr
		s.metrics.parseErrors.Add(ctx, 1)
		span.RecordError(err)
		span.SetStatus(codes.Error, "parse store file")
		telemetry.Error(ctx, "failed to parse entity store file",
			otellog.String("store.path", s.path),
			otellog.String("error", err.Error()),
			otellog.Int64("store.file_bytes", int64(len(raw))),
		)
		return nil, err
	}
	if sf.Entities == nil {
		sf.Entities = map[string]Entity{}
	}
	span.SetAttributes(attribute.Int("entity.count", len(sf.Entities)))
	return sf.Entities, nil
}

// save json.Marshals the WHOLE entity map and writes it back to the file
// NON-ATOMICALLY (os.WriteFile straight onto the target path — no
// temp-file-then-rename, no locking). This is the O(n) write path (F1) and the
// tear point (F3): two concurrent savers can interleave their writes and leave
// the file half-written. On success it refreshes the denormalized count so the
// gauge reflects reality.
func (s *Store) save(ctx context.Context, entities map[string]Entity) (err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.save",
		trace.WithAttributes(attribute.Int("entity.count", len(entities))),
	)
	defer span.End()
	defer s.observe(ctx, "Save", start, &err)

	raw, err := json.Marshal(storeFile{Entities: entities})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "marshal store file")
		return err
	}
	span.SetAttributes(attribute.Int64("entity.store.file_bytes", int64(len(raw))))

	// Non-atomic write: no temp+rename, no flock. Deliberate.
	if err = os.WriteFile(s.path, raw, 0o644); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "write store file")
		return err
	}
	s.count.Store(int64(len(entities)))
	return nil
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
func (s *Store) Create(ctx context.Context, in Input) (_ Entity, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Create",
		trace.WithAttributes(
			attribute.String("entity.type", in.Type),
			attribute.Int("entity.attributes.count", len(in.Attributes)),
		),
	)
	defer span.End()
	defer s.observe(ctx, "Create", start, &err)

	entities, err := s.load(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load")
		return Entity{}, err
	}

	id, err := newID()
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "generate id")
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

	if err = s.save(ctx, entities); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save")
		return Entity{}, err
	}
	span.SetAttributes(
		attribute.String("entity.id", id),
		attribute.Int("entity.count", len(entities)),
		attribute.Int64("entity.store.file_bytes", s.fileSize()),
	)
	return e, nil
}

// Get reads the whole file and serves one entity. ErrNotFound if unknown.
func (s *Store) Get(ctx context.Context, id string) (_ Entity, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Get",
		trace.WithAttributes(attribute.String("entity.id", id)),
	)
	defer span.End()
	defer s.observe(ctx, "Get", start, &err)

	entities, err := s.load(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load")
		return Entity{}, err
	}
	span.SetAttributes(attribute.Int("entity.count", len(entities)))

	e, ok := entities[id]
	if !ok {
		err = ErrNotFound
		span.RecordError(err)
		span.SetStatus(codes.Error, "entity not found")
		return Entity{}, err
	}
	span.SetAttributes(attribute.String("entity.type", e.Type))
	return e, nil
}

// Update fully replaces name/type/status/attributes, increments version, and
// bumps updatedAt. 404 if missing. Reads+mutates+writes the whole file.
func (s *Store) Update(ctx context.Context, id string, in Input) (_ Entity, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Update",
		trace.WithAttributes(
			attribute.String("entity.id", id),
			attribute.String("entity.type", in.Type),
			attribute.Int("entity.attributes.count", len(in.Attributes)),
		),
	)
	defer span.End()
	defer s.observe(ctx, "Update", start, &err)

	entities, err := s.load(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load")
		return Entity{}, err
	}
	span.SetAttributes(attribute.Int("entity.count", len(entities)))

	e, ok := entities[id]
	if !ok {
		err = ErrNotFound
		span.RecordError(err)
		span.SetStatus(codes.Error, "entity not found")
		return Entity{}, err
	}

	e.Name = in.Name
	e.Type = in.Type
	e.Status = in.Status
	e.Attributes = in.Attributes
	e.Version++
	e.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	entities[id] = e

	if err = s.save(ctx, entities); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save")
		return Entity{}, err
	}
	span.SetAttributes(
		attribute.Int("entity.version", e.Version),
		attribute.Int64("entity.store.file_bytes", s.fileSize()),
	)
	return e, nil
}

// Delete removes an entity. ErrNotFound if missing. Reads+mutates+writes whole.
func (s *Store) Delete(ctx context.Context, id string) (err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Delete",
		trace.WithAttributes(attribute.String("entity.id", id)),
	)
	defer span.End()
	defer s.observe(ctx, "Delete", start, &err)

	entities, err := s.load(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load")
		return err
	}
	span.SetAttributes(attribute.Int("entity.count", len(entities)))

	if _, ok := entities[id]; !ok {
		err = ErrNotFound
		span.RecordError(err)
		span.SetStatus(codes.Error, "entity not found")
		return err
	}
	delete(entities, id)

	if err = s.save(ctx, entities); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save")
		return err
	}
	span.SetAttributes(attribute.Int64("entity.store.file_bytes", s.fileSize()))
	return nil
}

// List returns every entity. THIS is the O(n) slow path: it unmarshals the
// whole file and marshals every entity. NO pagination, NO cap. Sorted by id for
// stable output.
func (s *Store) List(ctx context.Context) (_ []Entity, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.List")
	defer span.End()
	defer s.observe(ctx, "List", start, &err)

	entities, err := s.load(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "load")
		return nil, err
	}

	out := make([]Entity, 0, len(entities))
	for _, e := range entities {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })

	span.SetAttributes(
		attribute.Int("entity.count", len(out)),
		attribute.Int64("entity.store.file_bytes", s.fileSize()),
	)
	return out, nil
}

// Wipe resets the store to empty by writing {"entities":{}}. TRUSTED: must be
// correct.
func (s *Store) Wipe(ctx context.Context) (err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Wipe")
	defer span.End()
	defer s.observe(ctx, "Wipe", start, &err)

	if err = s.save(ctx, map[string]Entity{}); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "save")
		return err
	}
	return nil
}
