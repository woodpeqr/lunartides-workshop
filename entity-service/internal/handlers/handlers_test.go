package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/woodpeqr/lunartides-workshop/entity-service/internal/store"
)

// These tests assert the routes and response shapes under normal, serial use.

func newServer(t *testing.T) *httptest.Server {
	t.Helper()
	h := New(store.New(filepath.Join(t.TempDir(), "entities.json")))
	mux := http.NewServeMux()
	h.Register(mux)
	return httptest.NewServer(mux)
}

func decode(t *testing.T, r *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
	_ = r.Body.Close()
}

func TestHealthz(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp.StatusCode)
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["status"] != "ok" {
		t.Fatalf("healthz body: %v", body)
	}
}

func TestCreateEntityWireShape(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/entities", "application/json",
		strings.NewReader(`{"name":"rack1-sw3","type":"switch","status":"active","attributes":{"site":"dc1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d want 201", resp.StatusCode)
	}
	var e store.Entity
	decode(t, resp, &e)
	if e.ID == "" || !strings.HasPrefix(e.ID, "ent_") {
		t.Fatalf("expected ent_ id, got %q", e.ID)
	}
	if e.Name != "rack1-sw3" || e.Type != "switch" || e.Status != "active" {
		t.Fatalf("field mismatch: %+v", e)
	}
	if e.Version != 1 {
		t.Fatalf("expected version 1, got %d", e.Version)
	}
	if e.CreatedAt == "" || e.UpdatedAt == "" {
		t.Fatalf("expected timestamps: %+v", e)
	}
	if e.Attributes["site"] != "dc1" {
		t.Fatalf("attributes not preserved: %+v", e.Attributes)
	}
}

func TestCreateEntityBadBody(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/entities", "application/json",
		strings.NewReader(`{not json`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", resp.StatusCode)
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["error"] == "" {
		t.Fatalf("expected error envelope, got %v", body)
	}
}

func TestGetEntityRoundTripAndNotFound(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/entities", "application/json",
		strings.NewReader(`{"name":"n","type":"router","status":"active","attributes":{}}`))
	var created store.Entity
	decode(t, resp, &created)

	resp2, err := http.Get(srv.URL + "/entities/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp2.StatusCode)
	}
	var got store.Entity
	decode(t, resp2, &got)
	if got.ID != created.ID || got.Type != "router" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}

	resp3, _ := http.Get(srv.URL + "/entities/ent_missing")
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown id: got %d want 404", resp3.StatusCode)
	}
	var errBody map[string]string
	decode(t, resp3, &errBody)
	if errBody["error"] == "" {
		t.Fatal("expected error envelope on 404")
	}
}

func TestUpdateEntityWireShape(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/entities", "application/json",
		strings.NewReader(`{"name":"old","type":"switch","status":"active","attributes":{"a":"1"}}`))
	var created store.Entity
	decode(t, resp, &created)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/entities/"+created.ID,
		strings.NewReader(`{"name":"new","type":"firewall","status":"maintenance","attributes":{"b":"2"}}`))
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("update status: got %d want 200", resp2.StatusCode)
	}
	var updated store.Entity
	decode(t, resp2, &updated)
	if updated.Version != 2 {
		t.Fatalf("expected version 2, got %d", updated.Version)
	}
	if updated.Name != "new" || updated.Type != "firewall" || updated.Status != "maintenance" {
		t.Fatalf("update fields mismatch: %+v", updated)
	}
	if _, ok := updated.Attributes["a"]; ok {
		t.Fatalf("update should fully replace attributes: %+v", updated.Attributes)
	}

	// Update missing -> 404.
	reqNF, _ := http.NewRequest(http.MethodPut, srv.URL+"/entities/ent_missing",
		strings.NewReader(`{"name":"x","type":"y","status":"z","attributes":{}}`))
	respNF, _ := http.DefaultClient.Do(reqNF)
	if respNF.StatusCode != http.StatusNotFound {
		t.Fatalf("update missing: got %d want 404", respNF.StatusCode)
	}
}

func TestDeleteEntityWireShape(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/entities", "application/json",
		strings.NewReader(`{"name":"n","type":"ap","status":"active","attributes":{}}`))
	var created store.Entity
	decode(t, resp, &created)

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/entities/"+created.ID, nil)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("delete status: got %d want 200", resp2.StatusCode)
	}
	var body map[string]bool
	decode(t, resp2, &body)
	if !body["ok"] {
		t.Fatalf("delete body: %v", body)
	}

	// Gone after delete.
	gresp, _ := http.Get(srv.URL + "/entities/" + created.ID)
	if gresp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-delete get: got %d want 404", gresp.StatusCode)
	}

	// Delete missing -> 404.
	reqNF, _ := http.NewRequest(http.MethodDelete, srv.URL+"/entities/ent_missing", nil)
	respNF, _ := http.DefaultClient.Do(reqNF)
	if respNF.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing: got %d want 404", respNF.StatusCode)
	}
}

func TestListEntitiesWireShape(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	for _, name := range []string{"a", "b", "c"} {
		http.Post(srv.URL+"/entities", "application/json",
			strings.NewReader(`{"name":"`+name+`","type":"server","status":"active","attributes":{}}`))
	}

	resp, err := http.Get(srv.URL + "/entities")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status: got %d want 200", resp.StatusCode)
	}
	var body struct {
		Entities []store.Entity `json:"entities"`
	}
	decode(t, resp, &body)
	if body.Entities == nil {
		t.Fatal("entities key must be present, non-nil")
	}
	if len(body.Entities) != 3 {
		t.Fatalf("expected 3 entities, got %d", len(body.Entities))
	}
}

func TestWipeResetsState(t *testing.T) {
	srv := newServer(t)
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/entities", "application/json",
		strings.NewReader(`{"name":"gone","type":"switch","status":"active","attributes":{}}`))
	var created store.Entity
	decode(t, resp, &created)

	wresp, err := http.Post(srv.URL+"/wipe", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if wresp.StatusCode != http.StatusOK {
		t.Fatalf("wipe status: %d", wresp.StatusCode)
	}
	var wbody map[string]bool
	decode(t, wresp, &wbody)
	if !wbody["ok"] {
		t.Fatalf("wipe body: %v", wbody)
	}

	// Entity is gone after wipe.
	gresp, _ := http.Get(srv.URL + "/entities/" + created.ID)
	if gresp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-wipe get: got %d want 404", gresp.StatusCode)
	}
}
