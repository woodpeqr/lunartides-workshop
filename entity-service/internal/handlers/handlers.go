// Package handlers holds one HTTP handler per entity REST route.
//
// Error shape for all 4xx/5xx: {"error":"<message>"}.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/store"
)

// Handlers bundles the dependencies each route closure needs.
type Handlers struct {
	Store *store.Store
}

// New returns a Handlers backed by the given store.
func New(st *store.Store) *Handlers {
	return &Handlers{Store: st}
}

// Register wires every entity route onto the mux. Uses Go 1.22+ method+pattern
// routing ("METHOD /path/{wildcard}").
func (h *Handlers) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /entities", h.CreateEntity)
	mux.HandleFunc("GET /entities", h.ListEntities)
	mux.HandleFunc("GET /entities/{id}", h.GetEntity)
	mux.HandleFunc("PUT /entities/{id}", h.UpdateEntity)
	mux.HandleFunc("DELETE /entities/{id}", h.DeleteEntity)
	mux.HandleFunc("POST /wipe", h.Wipe)
	mux.HandleFunc("GET /healthz", h.Healthz)
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

// fail writes the error envelope with the mapped status.
func fail(w http.ResponseWriter, status int, err error) {
	writeError(w, status, err.Error())
}

// --- POST /entities --------------------------------------------------------
// Request {"name","type","status","attributes"} -> 201 {entity}.
func (h *Handlers) CreateEntity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var in store.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	e, err := h.Store.Create(ctx, in)
	if err != nil {
		fail(w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// --- GET /entities/{id} ----------------------------------------------------
// -> 200 {entity} or 404 {"error":"not found"}.
func (h *Handlers) GetEntity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := r.PathValue("id")
	e, err := h.Store.Get(ctx, id)
	if err != nil {
		fail(w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// --- PUT /entities/{id} ----------------------------------------------------
// Request {"name","type","status","attributes"} (full replace) -> 200 {entity}
// with version incremented, updatedAt bumped. 404 if missing.
func (h *Handlers) UpdateEntity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := r.PathValue("id")
	var in store.Input
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, errors.New("invalid request body"))
		return
	}
	e, err := h.Store.Update(ctx, id, in)
	if err != nil {
		fail(w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// --- DELETE /entities/{id} -------------------------------------------------
// -> 200 {"ok":true} or 404.
func (h *Handlers) DeleteEntity(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := r.PathValue("id")
	if err := h.Store.Delete(ctx, id); err != nil {
		fail(w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- GET /entities ---------------------------------------------------------
// List ALL -> 200 {"entities":[{entity},...]}.
func (h *Handlers) ListEntities(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	entities, err := h.Store.List(ctx)
	if err != nil {
		fail(w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

// --- POST /wipe ------------------------------------------------------------
// -> 200 {"ok":true}. TRUSTED: must be correct.
func (h *Handlers) Wipe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.Store.Wipe(ctx); err != nil {
		fail(w, errStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- GET /healthz ----------------------------------------------------------
// -> 200 {"status":"ok"}. Pure liveness.
func (h *Handlers) Healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
