// Package handlers holds one HTTP handler per CONTRACT §3 route (R1–R10).
//
// Wire shapes (§3) are FROZEN. A bug changes behavior/values, never the shape
// of a successful response. Error shape for all 4xx/5xx: {"error":"<message>"}.
//
// vcs base signal is minimal by design (CONTRACT §5): the OTel plumbing is
// pre-wired, but emitting spans/metrics/logs from these handlers IS the
// workshop. Search "TODO(workshop)" for where signal belongs.
//
// wipe and healthz are TRUSTED (§5) and are implemented minimally.
package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"

	"github.com/woodpeqr/lunartides-workshop/vcs/internal/store"
)

// Handlers bundles the dependencies each route closure needs.
type Handlers struct {
	Store *store.Store
}

// New returns a Handlers backed by the given store.
func New(st *store.Store) *Handlers {
	return &Handlers{Store: st}
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

// --- R1: POST /objects -----------------------------------------------------
// Request {"content":"<string>"} -> 201 {"hash":"<hex>"}.
func (h *Handlers) CreateObject(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add child spans / write metrics around PutObject.
	var req struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	hash, err := h.Store.PutObject([]byte(req.Content))
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"hash": hash})
}

// --- R2: GET /objects/{hash} -----------------------------------------------
// -> 200 {"content":"<string>"}.
func (h *Handlers) GetObject(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add signal.
	content, err := h.Store.GetObject(r.PathValue("hash"))
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"content": string(content)})
}

// --- R3: POST /commits -----------------------------------------------------
// Request {"files":{...},"parent":"<id|>","message":"<str>"} -> 201 {"id":"<hex>"}.
func (h *Handlers) CreateCommit(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add signal.
	var req struct {
		Files   map[string]string `json:"files"`
		Parent  string            `json:"parent"`
		Message string            `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	id, err := h.Store.PutCommit(req.Files, req.Parent, req.Message)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": id})
}

// --- R4: GET /commits/{id} -------------------------------------------------
// -> 200 {"id","parent","message","files":{path:hash}}.
func (h *Handlers) GetCommit(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add signal.
	c, err := h.Store.GetCommit(r.PathValue("id"))
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// --- R5: PUT /refs/{name} --------------------------------------------------
// Request {"commit":"<id>"} -> 200 {"name","commit"}.
func (h *Handlers) SetRef(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add signal.
	name := r.PathValue("name")
	var req struct {
		Commit string `json:"commit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.Store.SetRef(name, req.Commit); err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "commit": req.Commit})
}

// --- R6: GET /refs/{name} --------------------------------------------------
// -> 200 {"name","commit":"<id>"}.
func (h *Handlers) GetRef(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add signal.
	name := r.PathValue("name")
	commit, err := h.Store.GetRef(name)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name, "commit": commit})
}

// --- R7: GET /checkout/{ref} -----------------------------------------------
// ref = ref-name OR commit id -> 200 {"files":{"<path>":"<content>"}}.
func (h *Handlers) Checkout(w http.ResponseWriter, r *http.Request) {
	// TODO(workshop): add child spans / duration histogram around Checkout.
	files, err := h.Store.Checkout(r.PathValue("ref"))
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

// --- R8: POST /wipe --------------------------------------------------------
// -> 200 {"ok":true}. TRUSTED (CONTRACT §5): must be correct.
func (h *Handlers) Wipe(w http.ResponseWriter, r *http.Request) {
	h.Store.Wipe()
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
	// TODO(workshop, stretch): add per-path classification logs.
	a := r.URL.Query().Get("a")
	b := r.URL.Query().Get("b")

	filesA, err := h.Store.Checkout(a)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	filesB, err := h.Store.Checkout(b)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
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

	writeJSON(w, http.StatusOK, map[string][]string{
		"added":   added,
		"removed": removed,
		"changed": changed,
	})
}
