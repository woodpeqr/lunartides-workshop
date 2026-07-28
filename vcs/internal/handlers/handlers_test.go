package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/woodpeqr/lunartides-workshop/vcs/internal/store"
)

// These tests assert wire-shape stability (CONTRACT §3) and the trusted
// utilities. They use only ASCII, non-empty, single-commit, serial inputs so
// the planted telemetry-only bugs stay dormant (PLAN §5).

func newServer() *httptest.Server {
	h := New(store.New())
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
	srv := newServer()
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

func TestCreateObjectWireShape(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/objects", "application/json",
		strings.NewReader(`{"content":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status: got %d want 201", resp.StatusCode)
	}
	var body map[string]string
	decode(t, resp, &body)
	if body["hash"] == "" {
		t.Fatalf("expected hash key, got %v", body)
	}
}

func TestCreateObjectBadBody(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/objects", "application/json",
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

func TestGetObjectRoundTripAndNotFound(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/objects", "application/json",
		strings.NewReader(`{"content":"payload"}`))
	var created map[string]string
	decode(t, resp, &created)

	resp2, err := http.Get(srv.URL + "/objects/" + created["hash"])
	if err != nil {
		t.Fatal(err)
	}
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d", resp2.StatusCode)
	}
	var got map[string]string
	decode(t, resp2, &got)
	if got["content"] != "payload" {
		t.Fatalf("content mismatch: %v", got)
	}

	resp3, _ := http.Get(srv.URL + "/objects/deadbeef")
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown hash: got %d want 404", resp3.StatusCode)
	}
	var errBody map[string]string
	decode(t, resp3, &errBody)
	if errBody["error"] == "" {
		t.Fatal("expected error envelope on 404")
	}
}

func TestCommitCheckoutWireShape(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/commits", "application/json",
		strings.NewReader(`{"files":{"README":"hi"},"parent":"","message":"init"}`))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("commit status: got %d want 201", resp.StatusCode)
	}
	var commit map[string]string
	decode(t, resp, &commit)
	id := commit["id"]
	if id == "" {
		t.Fatal("expected commit id")
	}

	// GET commit metadata shape.
	resp2, _ := http.Get(srv.URL + "/commits/" + id)
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("get commit status: %d", resp2.StatusCode)
	}
	var meta struct {
		ID      string            `json:"id"`
		Parent  string            `json:"parent"`
		Message string            `json:"message"`
		Files   map[string]string `json:"files"`
	}
	decode(t, resp2, &meta)
	if meta.ID != id || meta.Message != "init" {
		t.Fatalf("commit metadata mismatch: %+v", meta)
	}
	if _, ok := meta.Files["README"]; !ok {
		t.Fatal("expected README in files map")
	}

	// Checkout shape.
	resp3, _ := http.Get(srv.URL + "/checkout/" + id)
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("checkout status: %d", resp3.StatusCode)
	}
	var co struct {
		Files map[string]string `json:"files"`
	}
	decode(t, resp3, &co)
	if co.Files["README"] != "hi" {
		t.Fatalf("checkout round-trip mismatch: %v", co.Files)
	}
}

func TestRefWireShape(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/refs/main",
		strings.NewReader(`{"commit":"abc123"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setref status: %d", resp.StatusCode)
	}
	var setBody map[string]string
	decode(t, resp, &setBody)
	if setBody["name"] != "main" || setBody["commit"] != "abc123" {
		t.Fatalf("setref body: %v", setBody)
	}

	resp2, _ := http.Get(srv.URL + "/refs/main")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("getref status: %d", resp2.StatusCode)
	}
	var getBody map[string]string
	decode(t, resp2, &getBody)
	if getBody["name"] != "main" || getBody["commit"] != "abc123" {
		t.Fatalf("getref body: %v", getBody)
	}

	resp3, _ := http.Get(srv.URL + "/refs/missing")
	if resp3.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown ref: got %d want 404", resp3.StatusCode)
	}
}

func TestWipeResetsState(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	resp, _ := http.Post(srv.URL+"/objects", "application/json",
		strings.NewReader(`{"content":"gone soon"}`))
	var created map[string]string
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

	// Object is gone after wipe.
	gresp, _ := http.Get(srv.URL + "/objects/" + created["hash"])
	if gresp.StatusCode != http.StatusNotFound {
		t.Fatalf("post-wipe get: got %d want 404", gresp.StatusCode)
	}
}

func TestDiffWireShape(t *testing.T) {
	srv := newServer()
	defer srv.Close()

	// Two commits with disjoint + shared-identical paths (bug-dormant: no
	// shared path with differing content).
	r1, _ := http.Post(srv.URL+"/commits", "application/json",
		strings.NewReader(`{"files":{"a":"1","shared":"same"},"parent":"","message":"c1"}`))
	var c1 map[string]string
	decode(t, r1, &c1)
	r2, _ := http.Post(srv.URL+"/commits", "application/json",
		strings.NewReader(`{"files":{"b":"2","shared":"same"},"parent":"","message":"c2"}`))
	var c2 map[string]string
	decode(t, r2, &c2)

	resp, err := http.Get(srv.URL + "/diff?a=" + c1["id"] + "&b=" + c2["id"])
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("diff status: %d", resp.StatusCode)
	}
	var body struct {
		Added   []string `json:"added"`
		Removed []string `json:"removed"`
		Changed []string `json:"changed"`
	}
	decode(t, resp, &body)
	// Shape assertion: keys present and non-nil slices. Values: b added, a removed.
	if body.Added == nil || body.Removed == nil || body.Changed == nil {
		t.Fatalf("diff slices must be non-nil: %+v", body)
	}
}
