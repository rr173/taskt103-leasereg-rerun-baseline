package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"leasereg/internal/lease"
	"leasereg/internal/store"
)

func newTestServer(t *testing.T, at time.Time) (*httptest.Server, *lease.MockClock) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(filepath.Join(dir, "http.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clk := lease.NewMockClock(at)
	mgr := lease.NewManager(s, clk)
	srv := New(mgr)
	router := NewRouter(srv)
	ts := httptest.NewServer(router)
	t.Cleanup(func() { ts.Close(); s.Close() })
	return ts, clk
}

// do performs an HTTP request and returns the status code plus the decoded
// JSON body (an object as map[string]any, an array as []any, or nil).
func do(t *testing.T, ts *httptest.Server, method, path string, body any) (int, any) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out any
	if len(data) > 0 {
		_ = json.Unmarshal(data, &out)
	}
	return resp.StatusCode, out
}

func obj(t *testing.T, body any) map[string]any {
	t.Helper()
	m, ok := body.(map[string]any)
	if !ok {
		t.Fatalf("body is %T, want map: %v", body, body)
	}
	return m
}

func arr(t *testing.T, body any) []any {
	t.Helper()
	a, ok := body.([]any)
	if !ok {
		t.Fatalf("body is %T, want array: %v", body, body)
	}
	return a
}

func TestHTTPAcquireAndInfo(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, body := do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	if status != 200 {
		t.Fatalf("status=%d body=%v", status, body)
	}
	b := obj(t, body)
	if b["fencing_token"].(float64) != 1 {
		t.Fatalf("token=%v want 1", b["fencing_token"])
	}
	if b["expires_at"].(float64) != 1060 {
		t.Fatalf("expires_at=%v want 1060", b["expires_at"])
	}
	status, body = do(t, ts, "GET", "/info?resource=R", nil)
	if status != 200 || obj(t, body)["holder"] != "alice" {
		t.Fatalf("info status=%d body=%v", status, body)
	}
}

func TestHTTPAcquireConflict(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	status, body := do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "bob", TTLSeconds: 60})
	if status != 409 {
		t.Fatalf("status=%d want 409", status)
	}
	conf, ok := obj(t, body)["conflict"].(map[string]any)
	if !ok || conf["holder"] != "alice" {
		t.Fatalf("conflict=%v", body)
	}
}

func TestHTTPRenewExpired(t *testing.T) {
	ts, clk := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	clk.Advance(120 * time.Second)
	status, body := do(t, ts, "POST", "/renew", renewRequest{Resource: "R", Holder: "alice", FencingToken: 1, TTLSeconds: 60})
	if status != 410 {
		t.Fatalf("status=%d want 410 body=%v", status, body)
	}
}

func TestHTTPRenewStaleToken(t *testing.T) {
	ts, clk := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	clk.Advance(120 * time.Second) // expire
	status, body := do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	if status != 200 || obj(t, body)["fencing_token"].(float64) != 2 {
		t.Fatalf("reacquire status=%d body=%v", status, body)
	}
	status, body = do(t, ts, "POST", "/renew", renewRequest{Resource: "R", Holder: "alice", FencingToken: 1, TTLSeconds: 60})
	if status != 403 {
		t.Fatalf("stale renew status=%d want 403 body=%v", status, body)
	}
}

func TestHTTPReleaseAndList(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "A", Holder: "h1", TTLSeconds: 60})
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "B", Holder: "h2", TTLSeconds: 30})
	status, body := do(t, ts, "GET", "/leases", nil)
	if status != 200 {
		t.Fatalf("list status=%d", status)
	}
	if len(arr(t, body)) != 2 {
		t.Fatalf("list len=%d want 2", len(arr(t, body)))
	}
	status, _ = do(t, ts, "POST", "/release", releaseRequest{Resource: "A", Holder: "h1", FencingToken: 1})
	if status != 204 {
		t.Fatalf("release status=%d want 204", status)
	}
	_, body = do(t, ts, "GET", "/leases", nil)
	if len(arr(t, body)) != 1 {
		t.Fatalf("after release list len=%d want 1", len(arr(t, body)))
	}
}

func TestHTTPSweep(t *testing.T) {
	ts, clk := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "A", Holder: "h", TTLSeconds: 60})
	clk.Advance(120 * time.Second)
	status, body := do(t, ts, "POST", "/admin/sweep", nil)
	if status != 200 {
		t.Fatalf("sweep status=%d", status)
	}
	if obj(t, body)["swept"].(float64) != 1 {
		t.Fatalf("swept=%v want 1", obj(t, body)["swept"])
	}
}

func TestHTTPInfoMissing(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, _ := do(t, ts, "GET", "/info?resource=none", nil)
	if status != 404 {
		t.Fatalf("status=%d want 404", status)
	}
}

func TestHTTPValidation(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, body := do(t, ts, "POST", "/acquire", acquireRequest{Resource: "", Holder: "a", TTLSeconds: 60})
	if status != 400 {
		t.Fatalf("status=%d want 400 body=%v", status, body)
	}
	status, _ = do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "a", TTLSeconds: 0})
	if status != 400 {
		t.Fatalf("status=%d want 400", status)
	}
}

func TestHTTPBadJSON(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	req, _ := http.NewRequest("POST", ts.URL+"/acquire", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d want 400", resp.StatusCode)
	}
}

func TestHTTPHealth(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, body := do(t, ts, "GET", "/health", nil)
	if status != 200 || obj(t, body)["status"] != "ok" {
		t.Fatalf("health status=%d body=%v", status, body)
	}
}

func TestHTTPMethodNotAllowed(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	req, _ := http.NewRequest("GET", ts.URL+"/acquire", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", resp.StatusCode)
	}
}

// keep context import used even if a sub-test is removed
var _ = context.Background
