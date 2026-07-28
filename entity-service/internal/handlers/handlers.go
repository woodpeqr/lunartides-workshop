// Package handlers holds one HTTP handler per entity REST route.
//
// Wire shapes are FROZEN. Error shape for all 4xx/5xx: {"error":"<message>"}.
//
// Telemetry (the workshop): each handler opens a child span under the request
// span, records errors on it, threads ctx into the store (so store spans nest),
// and emits trace-correlated otel/log records on lifecycle + every error path.
//
// wipe and healthz are TRUSTED and implemented minimally. healthz MUST NOT
// touch the store file (pure liveness).
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"

	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/store"
	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/telemetry"
)

// Handlers bundles the dependencies each route closure needs.
type Handlers struct {
	Store  *store.Store
	tracer trace.Tracer
}

// New returns a Handlers backed by the given store.
func New(st *store.Store) *Handlers {
	return &Handlers{Store: st, tracer: otel.Tracer("entity-service")}
}

// Register wires every entity route onto the mux. Uses Go 1.22+ method+pattern
// routing ("METHOD /path/{wildcard}").
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /entities", h.CreateEntity)        // create
	mux.HandleFunc("GET /entities", h.ListEntities)         // list (O(n) slow path)
	mux.HandleFunc("GET /entities/{id}", h.GetEntity)       // get
	mux.HandleFunc("PUT /entities/{id}", h.UpdateEntity)    // update
	mux.HandleFunc("DELETE /entities/{id}", h.DeleteEntity) // delete
	mux.HandleFunc("POST /wipe", h.Wipe)                    // wipe (trusted)
	mux.HandleFunc("GET /healthz", h.Healthz)               // healthz (trusted)
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

// errStatus maps a store error to its HTTP status.
func errStatus(err error) int {
	if errors.Is(err, store.ErrNotFound) {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// fail records err on span, logs it trace-correlated, and writes the error
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

// --- POST /entities --------------------------------------------------------
// Request {"name","type","status","attributes"} -> 201 {entity}.
func (h *Handlers) CreateEntity(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.CreateEntity")
	defer span.End()

	var in store.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(ctx, span, w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	span.SetAttributes(
		attribute.String("entity.type", in.Type),
		attribute.Int("entity.attributes.count", len(in.Attributes)),
	)
	e, err := h.Store.Create(ctx, in)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.String("entity.id", e.ID))
	telemetry.Info(ctx, "entity created",
		otellog.String("entity.id", e.ID),
		otellog.String("entity.type", e.Type),
		otellog.String("entity.name", e.Name),
	)
	writeJSON(w, http.StatusCreated, e)
}

// --- GET /entities/{id} ----------------------------------------------------
// -> 200 {entity} or 404 {"error":"not found"}.
func (h *Handlers) GetEntity(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.GetEntity")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("entity.id", id))
	e, err := h.Store.Get(ctx, id)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// --- PUT /entities/{id} ----------------------------------------------------
// Request {"name","type","status","attributes"} (full replace) -> 200 {entity}
// with version incremented, updatedAt bumped. 404 if missing.
func (h *Handlers) UpdateEntity(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.UpdateEntity")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("entity.id", id))
	var in store.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(ctx, span, w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	span.SetAttributes(
		attribute.String("entity.type", in.Type),
		attribute.Int("entity.attributes.count", len(in.Attributes)),
	)
	e, err := h.Store.Update(ctx, id, in)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.Int("entity.version", e.Version))
	telemetry.Info(ctx, "entity updated",
		otellog.String("entity.id", e.ID),
		otellog.Int("entity.version", e.Version),
	)
	writeJSON(w, http.StatusOK, e)
}

// --- DELETE /entities/{id} -------------------------------------------------
// -> 200 {"ok":true} or 404.
func (h *Handlers) DeleteEntity(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.DeleteEntity")
	defer span.End()

	id := r.PathValue("id")
	span.SetAttributes(attribute.String("entity.id", id))
	if err := h.Store.Delete(ctx, id); err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	telemetry.Info(ctx, "entity deleted",
		otellog.String("entity.id", id),
	)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- GET /entities ---------------------------------------------------------
// List ALL -> 200 {"entities":[{entity},...]}. O(n) slow path: no pagination,
// no cap.
func (h *Handlers) ListEntities(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.ListEntities")
	defer span.End()

	entities, err := h.Store.List(ctx)
	if err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	span.SetAttributes(attribute.Int("entity.count", len(entities)))
	writeJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

// --- POST /wipe ------------------------------------------------------------
// -> 200 {"ok":true}. TRUSTED: must be correct.
func (h *Handlers) Wipe(w http.ResponseWriter, r *http.Request) {
	ctx, span := h.tracer.Start(r.Context(), "handler.Wipe")
	defer span.End()

	if err := h.Store.Wipe(ctx); err != nil {
		fail(ctx, span, w, errStatus(err), err)
		return
	}
	telemetry.Warn(ctx, "store wiped: all entities cleared")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- GET /healthz ----------------------------------------------------------
// -> 200 {"status":"ok"}. TRUSTED: pure liveness, MUST NOT touch the store file.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
