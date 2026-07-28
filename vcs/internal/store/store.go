// Package store is the in-memory state of vcs (CONTRACT §2 domain model).
//
// All state is process-local and in-memory. `wipe` clears everything.
//
// NOTE: the store logic is DELIBERATELY BUGGY territory (CONTRACT §5) — the
// garbage to be observed. The wire shapes stay frozen; only values/behavior
// misbehave, and only for input classes the base tests never exercise.
//
// Telemetry (the workshop): every store method opens a child span under the
// caller's request span, records errors, and feeds the store metrics
// (object/byte/commit/ref gauges + per-op latency histogram + error counter).
// The byte gauge is the OOM signal — object flood shows up as resident growth.
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
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

	// residentBytes is a denormalized tally of raw object bytes held in memory.
	// This is the OOM signal exported by the vcs.store.bytes gauge: an object
	// flood shows up here as unbounded resident growth (telemetry only — it is
	// NOT a cap, nothing consults it to refuse a write).
	residentBytes int

	// checkoutCache memoizes reassembled snapshots by resolved commit id.
	// Commit ids are immutable, so a cached snapshot is never stale.
	checkoutCache map[string]map[string]string

	// tracer/metrics are the store's slice of the OTel signal surface, resolved
	// once from the globals Init installed.
	tracer  trace.Tracer
	metrics storeMetrics
}

// storeMetrics bundles the store instruments. Gauges are observable and read
// the live counters under RLock; the histogram/counter are recorded per call.
type storeMetrics struct {
	opDuration metric.Float64Histogram // vcs.store.operation.duration (ms)
	opErrors   metric.Int64Counter     // vcs.store.operation.errors
}

// New returns an initialized, empty Store with its OTel instruments installed.
func New() *Store {
	s := &Store{
		tracer: otel.Tracer("vcs"),
	}
	s.reset()
	s.initMetrics()
	return s
}

// initMetrics resolves the store instruments from the global meter and wires
// the observable gauges to read live store state. Errors here are non-fatal:
// under the noop provider (tests) the meter hands back valid no-op instruments.
func (s *Store) initMetrics() {
	meter := otel.Meter("vcs")

	s.metrics.opDuration, _ = meter.Float64Histogram(
		"vcs.store.operation.duration",
		metric.WithDescription("Latency of store operations."),
		metric.WithUnit("ms"),
	)
	s.metrics.opErrors, _ = meter.Int64Counter(
		"vcs.store.operation.errors",
		metric.WithDescription("Store operations that returned an error."),
	)

	// Observable gauges: sampled on collection, reading the live tallies. These
	// are the memory-pressure lens — object count and resident bytes climb as an
	// object flood fills the maps, and never fall until a wipe.
	objects, _ := meter.Int64ObservableGauge(
		"vcs.store.objects",
		metric.WithDescription("Distinct content-addressed objects held in memory."),
		metric.WithUnit("{object}"),
	)
	bytes, _ := meter.Int64ObservableGauge(
		"vcs.store.bytes",
		metric.WithDescription("Resident raw object bytes held in memory (OOM signal)."),
		metric.WithUnit("By"),
	)
	commits, _ := meter.Int64ObservableGauge(
		"vcs.store.commits",
		metric.WithDescription("Commits held in memory."),
		metric.WithUnit("{commit}"),
	)
	refs, _ := meter.Int64ObservableGauge(
		"vcs.store.refs",
		metric.WithDescription("Refs held in memory."),
		metric.WithUnit("{ref}"),
	)
	cacheEntries, _ := meter.Int64ObservableGauge(
		"vcs.store.checkout_cache.entries",
		metric.WithDescription("Reassembled snapshots memoized in the checkout cache."),
		metric.WithUnit("{entry}"),
	)

	// One callback reads every gauge under a single RLock for a consistent view.
	_, _ = meter.RegisterCallback(
		func(_ context.Context, o metric.Observer) error {
			s.mu.RLock()
			defer s.mu.RUnlock()
			o.ObserveInt64(objects, int64(s.objectCount))
			o.ObserveInt64(bytes, int64(s.residentBytes))
			o.ObserveInt64(commits, int64(len(s.commits)))
			o.ObserveInt64(refs, int64(len(s.refs)))
			o.ObserveInt64(cacheEntries, int64(len(s.checkoutCache)))
			return nil
		},
		objects, bytes, commits, refs, cacheEntries,
	)
}

// observe records an operation's latency and, on error, bumps the error
// counter. Deferred at the top of each store method with the op name.
func (s *Store) observe(ctx context.Context, op string, start time.Time, errp *error) {
	attrs := metric.WithAttributes(attribute.String("vcs.store.op", op))
	s.metrics.opDuration.Record(ctx, float64(time.Since(start).Microseconds())/1000.0, attrs)
	if errp != nil && *errp != nil {
		s.metrics.opErrors.Add(ctx, 1, attrs)
	}
}

// reset (re)initializes all maps. Shared by New and Wipe.
func (s *Store) reset() {
	s.objects = make(map[string][]byte)
	s.commits = make(map[string]Commit)
	s.refs = make(map[string]string)
	s.objectCount = 0
	s.residentBytes = 0
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
func (s *Store) PutObject(ctx context.Context, content []byte) (hash string, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.PutObject",
		trace.WithAttributes(attribute.Int("vcs.object.content_bytes", len(content))),
	)
	defer span.End()
	defer s.observe(ctx, "PutObject", start, &err)

	// Empty content hashes to a well-known value; there are no bytes to persist.
	if len(content) == 0 {
		h := hashContent(content)
		span.SetAttributes(attribute.String("vcs.object.hash", h), attribute.Bool("vcs.object.empty", true))
		return h, nil
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
			s.residentBytes += len(content)
		}
		s.mu.Unlock()
	}
	span.SetAttributes(
		attribute.String("vcs.object.hash", h),
		attribute.Bool("vcs.object.dedup_hit", exists),
	)
	return h, nil
}

// GetObject fetches raw content by hash (R2). ErrNotFound if unknown.
func (s *Store) GetObject(ctx context.Context, hash string) (content []byte, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.GetObject",
		trace.WithAttributes(attribute.String("vcs.object.hash", hash)),
	)
	defer span.End()
	defer s.observe(ctx, "GetObject", start, &err)

	s.mu.RLock()
	defer s.mu.RUnlock()
	b, ok := s.objects[hash]
	if !ok {
		span.RecordError(ErrNotFound)
		span.SetStatus(codes.Error, "object not found")
		return nil, ErrNotFound
	}
	span.SetAttributes(attribute.Int("vcs.object.content_bytes", len(b)))
	return b, nil
}

// PutCommit stores each file's inline content as an object (R1 semantics),
// records path->hash, computes the commit id, and stores the Commit (R3).
func (s *Store) PutCommit(ctx context.Context, files map[string]string, parent, message string) (id string, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.PutCommit",
		trace.WithAttributes(
			attribute.Int("vcs.commit.file_count", len(files)),
			attribute.String("vcs.commit.parent", parent),
		),
	)
	defer span.End()
	defer s.observe(ctx, "PutCommit", start, &err)

	fileHashes := make(map[string]string, len(files))
	for path, content := range files {
		h, err := s.PutObject(ctx, []byte(content))
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "put file object")
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
	span.SetAttributes(attribute.String("vcs.commit.id", id))
	return id, nil
}

// GetCommit returns commit metadata by id (R4). ErrNotFound if unknown.
func (s *Store) GetCommit(ctx context.Context, id string) (_ Commit, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.GetCommit",
		trace.WithAttributes(attribute.String("vcs.commit.id", id)),
	)
	defer span.End()
	defer s.observe(ctx, "GetCommit", start, &err)

	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.commits[id]
	if !ok {
		err = ErrNotFound
		span.RecordError(err)
		span.SetStatus(codes.Error, "commit not found")
		return Commit{}, err
	}
	span.SetAttributes(attribute.Int("vcs.commit.file_count", len(c.Files)))
	return c, nil
}

// SetRef points a ref name at a commit id (R5).
func (s *Store) SetRef(ctx context.Context, name, commitID string) (err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.SetRef",
		trace.WithAttributes(
			attribute.String("vcs.ref.name", name),
			attribute.String("vcs.commit.id", commitID),
		),
	)
	defer span.End()
	defer s.observe(ctx, "SetRef", start, &err)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.refs[name] = commitID
	return nil
}

// GetRef resolves a ref name to its commit id (R6). ErrNotFound if unknown.
func (s *Store) GetRef(ctx context.Context, name string) (commitID string, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.GetRef",
		trace.WithAttributes(attribute.String("vcs.ref.name", name)),
	)
	defer span.End()
	defer s.observe(ctx, "GetRef", start, &err)

	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.refs[name]
	if !ok {
		err = ErrNotFound
		span.RecordError(err)
		span.SetStatus(codes.Error, "ref not found")
		return "", err
	}
	span.SetAttributes(attribute.String("vcs.commit.id", id))
	return id, nil
}

// resolve maps a checkout argument to a commit id: a ref name first, else the
// argument is treated as a commit id (CONTRACT §6.4).
func (s *Store) resolve(ref string) (string, bool) {
	s.mu.RLock()
	id, ok := s.refs[ref]
	s.mu.RUnlock()
	if ok {
		return id, true
	}
	return ref, false
}

// Checkout reassembles path->content at a ref (R7). The ref argument is a
// ref-name first; if no such ref, it is treated as a commit id (CONTRACT §6.4).
// This is the round-trip oracle.
func (s *Store) Checkout(ctx context.Context, ref string) (files map[string]string, err error) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Checkout",
		trace.WithAttributes(attribute.String("vcs.checkout.ref", ref)),
	)
	defer span.End()
	defer s.observe(ctx, "Checkout", start, &err)

	cid, viaRef := s.resolve(ref)
	span.SetAttributes(
		attribute.String("vcs.commit.id", cid),
		attribute.Bool("vcs.checkout.via_ref", viaRef),
	)

	s.mu.RLock()
	if hit, ok := s.checkoutCache[cid]; ok {
		s.mu.RUnlock()
		span.SetAttributes(
			attribute.Bool("vcs.checkout.cache_hit", true),
			attribute.Int("vcs.checkout.file_count", len(hit)),
		)
		return hit, nil
	}
	s.mu.RUnlock()
	span.SetAttributes(attribute.Bool("vcs.checkout.cache_hit", false))

	files, err = s.reassemble(ctx, cid)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "reassemble")
		return nil, err
	}

	s.mu.Lock()
	s.checkoutCache[cid] = files
	s.mu.Unlock()
	span.SetAttributes(attribute.Int("vcs.checkout.file_count", len(files)))
	return files, nil
}

// reassemble builds the path->content snapshot for a commit id.
func (s *Store) reassemble(ctx context.Context, cid string) (_ map[string]string, err error) {
	_, span := s.tracer.Start(ctx, "store.reassemble",
		trace.WithAttributes(attribute.String("vcs.commit.id", cid)),
	)
	defer span.End()

	s.mu.RLock()
	defer s.mu.RUnlock()

	target, ok := s.commits[cid]
	if !ok {
		err = ErrNotFound
		span.RecordError(err)
		span.SetStatus(codes.Error, "commit not found")
		return nil, err
	}

	// Resolve the full commit ancestry so every reachable blob is paged in
	// before we assemble the target snapshot. Depth + bytes are the deep-chain
	// lens: a long parent chain shows up as span attributes here.
	historyBytes := 0
	historyDepth := 0
	for id := cid; id != ""; {
		c, ok := s.commits[id]
		if !ok {
			break
		}
		historyDepth++
		for _, hash := range c.Files {
			if b, ok := s.objects[hash]; ok {
				historyBytes += len(b)
			}
		}
		id = c.Parent
	}
	span.SetAttributes(
		attribute.Int("vcs.reassemble.history_depth", historyDepth),
		attribute.Int("vcs.reassemble.history_bytes", historyBytes),
	)

	files := make(map[string]string, len(target.Files))
	for path, hash := range target.Files {
		b, ok := s.objects[hash]
		if !ok {
			// A referenced blob is missing: the commit is only partially
			// resolvable. Surface it rather than silently dropping the path.
			err = fmt.Errorf("commit %s references missing object %s for path %q", cid, hash, path)
			span.RecordError(err)
			span.SetStatus(codes.Error, "missing object")
			return nil, err
		}
		files[path] = string(b)
	}
	span.SetAttributes(attribute.Int("vcs.reassemble.file_count", len(files)))
	return files, nil
}

// Wipe resets ALL state (R8). CONTRACT §5: this MUST be correct.
func (s *Store) Wipe(ctx context.Context) {
	start := time.Now()
	ctx, span := s.tracer.Start(ctx, "store.Wipe")
	defer span.End()
	defer s.observe(ctx, "Wipe", start, nil)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reset()
}
