// Package handlers holds one HTTP handler per CONTRACT §3 route (R1–R10).
//
// Wire shapes (§3) are FROZEN. A bug changes behavior/values, never the shape
// of a successful response. Error shape for all 4xx/5xx: {"error":"<message>"}.
//
// Telemetry (the workshop): each handler opens a child span under the request
// span, records errors on it, threads ctx into the store (so store spans nest),
// and emits trace-correlated otel/log records on lifecycle + every error path.
//
// wipe and healthz are TRUSTED (§5) and are implemented minimally.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/woodpeqr/lunartides-workshop/vcs/internal/store"
	"github.com/woodpeqr/lunartides-workshop/vcs/internal/telemetry"
)

// Handlers bundles the dependencies each route closure needs.
type Handlers struct {
	Store  *store.Store
	tracer trace.Tracer
}

// New returns a Handlers backed by the given store.
func New(st *store.Store) *Handlers {
	return &Handlers{Store: st, tracer: otel.Tracer("vcs")}
}

// Register wires every CONTRACT §3 route onto the mux. Uses Go 1.22+
// method+pattern routing ("METHOD /path/{wildcard}").
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /objects", h.CreateObject)    // R1
	mux.HandleFunc("GET /objects/{hash}", h.GetObject) // R2
	mux.HandleFunc("POST /commits", h.CreateCommit)    // R3
	mux.HandleFunc("GET /commits/{id}", h.GetCommit)   // R4
	mux.HandleFunc("PUT /refs/{name}", h.SetRef)       // R5
	mux.HandleFunc("GET /refs/{name}", h.GetRef)       // R6
	mux.HandleFunc("GET /checkout/{ref}", h.Checkout)  // R7
	mux.HandleFunc("POST /wipe", h.Wipe)               // R8 (trusted)
	mux.HandleFunc("GET /healthz", h.Healthz)          // R9 (trusted)
	mux.HandleFunc("GET /diff", h.Diff)                // R10 (stretch)
}

// --- helpers ---------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// errStatus maps a store error to its HTTP status (CONTRACT §3).
func errStatus(err error) int {
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// fail records err on span, logs it trace-correlated, and writes the §3 error
// envelope. Warn for expected 4xx (not-found/bad-request), Error for 5xx.
func fail(ctx context.Context, span trace.Span, w http.ResponseWriter, status int, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	attrs := []otellog.KeyValue{
		otellog.Int("http.status", status),
		otellog.String("error", err.Error()),
	}
	if status >= 500 {
		telemetry.Error(ctx, "handler failed", attrs...)
	} else {
		telemetry.Warn(ctx, "handler rejected request", attrs...)
	}
	writeError(w, status, err.Error())
}

// --- R1: POST /objects -----------------------------------------------------
// Request {"content":"<string>"} -> 201 {"hash":"<hex>"}.
func (h *Handlers) CreateObject(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.CreateObject")
	defer span.End()

	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(ctx, span, w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	span.SetAttributes(attribute.Int("vcs.object.content_bytes", len(req.Content)))
	hash, err := h.Store.PutObject(ctx, []byte(req.Content))
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.String("vcs.object.hash", hash))
	writeJSON(w, http.StatusCreated, map[string]string{"hash": hash})
}

// --- R2: GET /objects/{hash} -----------------------------------------------
// -> 200 {"content":"<string>"}.
func (h *Handlers) GetObject(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.GetObject")
	defer span.End()

	hash := r.PathValue("hash")
	span.SetAttributes(attribute.String("vcs.object.hash", hash))
	content, err := h.Store.GetObject(ctx, hash)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": string(content)})
}

// --- R3: POST /commits -----------------------------------------------------
// Request {"files":{...},"parent":"<id|>","message":"<str>"} -> 201 {"id":"<hex>"}.
func (h *Handlers) CreateCommit(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.CreateCommit")
	defer span.End()

	var req struct {
		Files   map[string]string `json:"files"`
		Parent  string            `json:"parent"`
		Message string            `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(ctx, span, w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	span.SetAttributes(
		attribute.Int("vcs.commit.file_count", len(req.Files)),
		attribute.String("vcs.commit.parent", req.Parent),
	)
	id, err := h.Store.PutCommit(ctx, req.Files, req.Parent, req.Message)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.String("vcs.commit.id", id))
	telemetry.Info(ctx, "commit created",
		otellog.String("commit.id", id),
		otellog.Int("commit.file_count", len(req.Files)),
	)
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// --- R4: GET /commits/{id} -------------------------------------------------
// -> 200 {"id","parent","message","files":{path:hash}}.
func (h *Handlers) GetCommit(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.GetCommit")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("vcs.commit.id", id))
	c, err := h.Store.GetCommit(ctx, id)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// --- R5: PUT /refs/{name} --------------------------------------------------
// Request {"commit":"<id>"} -> 200 {"name","commit"}.
func (h *Handlers) SetRef(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.SetRef")
	defer span.End()

	name := r.PathValue("name")
	span.SetAttributes(attribute.String("vcs.ref.name", name))
	var req struct {
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fail(ctx, span, w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	span.SetAttributes(attribute.String("vcs.commit.id", req.Commit))
	if err := h.Store.SetRef(ctx, name, req.Commit); err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	telemetry.Info(ctx, "ref updated",
		otellog.String("ref.name", name),
		otellog.String("commit.id", req.Commit),
	)
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "commit": req.Commit})
}

// --- R6: GET /refs/{name} --------------------------------------------------
// -> 200 {"name","commit":"<id>"}.
func (h *Handlers) GetRef(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.GetRef")
	defer span.End()

	name := r.PathValue("name")
	span.SetAttributes(attribute.String("vcs.ref.name", name))
	commit, err := h.Store.GetRef(ctx, name)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.String("vcs.commit.id", commit))
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "commit": commit})
}

// --- R7: GET /checkout/{ref} -----------------------------------------------
// ref = ref-name OR commit id -> 200 {"files":{"<path>":"<content>"}}.
func (h *Handlers) Checkout(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.Checkout")
	defer span.End()

	ref := r.PathValue("ref")
	span.SetAttributes(attribute.String("vcs.checkout.ref", ref))
	files, err := h.Store.Checkout(ctx, ref)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.Int("vcs.checkout.file_count", len(files)))
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// --- R8: POST /wipe --------------------------------------------------------
// -> 200 {"ok":true}. TRUSTED (CONTRACT §5): must be correct.
func (h *Handlers) Wipe(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.Wipe")
	defer span.End()

	h.Store.Wipe(ctx)
	telemetry.Warn(ctx, "store wiped: all state cleared")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- R9: GET /healthz ------------------------------------------------------
// -> 200 {"status":"ok"}. TRUSTED (CONTRACT §5): liveness must be trustworthy.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- R10 (stretch): GET /diff?a={ref}&b={ref} ------------------------------
// -> 200 {"added":[],"removed":[],"changed":[]}.
func (h *Handlers) Diff(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.Diff")
	defer span.End()

	a := r.URL.Query().Get("a")
	b := r.URL.Query().Get("b")
	span.SetAttributes(
		attribute.String("vcs.diff.a", a),
		attribute.String("vcs.diff.b", b),
	)

	filesA, err := h.Store.Checkout(ctx, a)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	filesB, err := h.Store.Checkout(ctx, b)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}

	added := []string{}
	removed := []string{}
	changed := []string{}

	for path, contentB := range filesB {
		contentA, inA := filesA[path]
		if !inA {
			added = append(added, path)
			continue
		}
		if contentA != contentB {
			// Present in both refs with differing content: a modification.
			changed = append(changed, path)
		}
	}
	for path := range filesA {
		if _, inB := filesB[path]; !inB {
			removed = append(removed, path)
		}
	}

	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)

	span.SetAttributes(
		attribute.Int("vcs.diff.added", len(added)),
		attribute.Int("vcs.diff.removed", len(removed)),
		attribute.Int("vcs.diff.changed", len(changed)),
	)
	writeJSON(w, http.StatusOK, map[string][]string{
		"added":   added,
		"removed": removed,
		"changed": changed,
	})
}
