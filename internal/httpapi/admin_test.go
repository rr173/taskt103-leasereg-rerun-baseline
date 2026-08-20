package httpapi

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHTTPTransfer(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	status, body := do(t, ts, "POST", "/transfer", transferRequest{
		Resource: "R", CurrentHolder: "alice", NewHolder: "bob", FencingToken: 1, TTLSeconds: 60,
	})
	if status != 200 {
		t.Fatalf("transfer status=%d body=%v", status, body)
	}
	b := obj(t, body)
	if b["holder"] != "bob" || b["fencing_token"].(float64) != 2 {
		t.Fatalf("transfer body=%v", body)
	}
	// alice cannot renew with old token
	status, _ = do(t, ts, "POST", "/renew", renewRequest{Resource: "R", Holder: "alice", FencingToken: 1, TTLSeconds: 60})
	if status != 403 {
		t.Fatalf("stale renew status=%d want 403", status)
	}
}

func TestHTTPBatchAcquire(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R2", Holder: "carol", TTLSeconds: 60})
	status, body := do(t, ts, "POST", "/batch/acquire", map[string]any{
		"requests": []map[string]any{
			{"resource": "R1", "holder": "alice", "ttl_seconds": 60},
			{"resource": "R2", "holder": "bob", "ttl_seconds": 60},
		},
	})
	if status != 200 {
		t.Fatalf("batch status=%d body=%v", status, body)
	}
	results := arr(t, body)
	if len(results) != 2 {
		t.Fatalf("results len=%d want 2", len(results))
	}
	r0 := results[0].(map[string]any)
	if r0["granted"] == nil {
		t.Fatalf("r0 should be granted: %v", r0)
	}
	r1 := results[1].(map[string]any)
	if r1["conflict"] == nil {
		t.Fatalf("r1 should be conflict: %v", r1)
	}
}

func TestHTTPLeaseDetailAndExpired(t *testing.T) {
	ts, clk := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	status, body := do(t, ts, "GET", "/leases/R", nil)
	if status != 200 || obj(t, body)["holder"] != "alice" {
		t.Fatalf("detail status=%d body=%v", status, body)
	}
	clk.Advance(120 * time.Second)
	status, body = do(t, ts, "GET", "/expired", nil)
	if status != 200 {
		t.Fatalf("expired status=%d", status)
	}
	if len(arr(t, body)) != 1 {
		t.Fatalf("expired len=%d want 1", len(arr(t, body)))
	}
}

func TestHTTPFencing(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	status, body := do(t, ts, "GET", "/fencing?resource=R", nil)
	if status != 200 {
		t.Fatalf("fencing status=%d", status)
	}
	if obj(t, body)["next_fencing_token"].(float64) != 2 {
		t.Fatalf("next token=%v want 2", obj(t, body)["next_fencing_token"])
	}
}

func TestHTTPHolderLeases(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "A", Holder: "alice", TTLSeconds: 60})
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "B", Holder: "alice", TTLSeconds: 60})
	status, body := do(t, ts, "GET", "/holders/alice/leases", nil)
	if status != 200 {
		t.Fatalf("holder leases status=%d", status)
	}
	if len(arr(t, body)) != 2 {
		t.Fatalf("holder leases len=%d want 2", len(arr(t, body)))
	}
}

func TestHTTPResourceCRUD(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	// register
	status, _ := do(t, ts, "POST", "/resources", resourceRequest{Name: "printer-1", MaxTTL: 100, Description: "lobby"})
	if status != 201 {
		t.Fatalf("register status=%d", status)
	}
	// get
	status, body := do(t, ts, "GET", "/resources/printer-1", nil)
	if status != 200 || obj(t, body)["max_ttl_seconds"].(float64) != 100 {
		t.Fatalf("get resource status=%d body=%v", status, body)
	}
	// list
	_, body = do(t, ts, "GET", "/resources", nil)
	if len(arr(t, body)) != 1 {
		t.Fatalf("list len=%d want 1", len(arr(t, body)))
	}
	// update
	status, _ = do(t, ts, "PUT", "/resources/printer-1", resourceRequest{Name: "printer-1", MaxTTL: 200, Description: "updated"})
	if status != 200 {
		t.Fatalf("update status=%d", status)
	}
	// max_ttl enforced on acquire
	status, _ = do(t, ts, "POST", "/acquire", acquireRequest{Resource: "printer-1", Holder: "alice", TTLSeconds: 201})
	if status != 400 {
		t.Fatalf("acquire over max_ttl status=%d want 400", status)
	}
	// delete
	status, _ = do(t, ts, "DELETE", "/resources/printer-1", nil)
	if status != 204 {
		t.Fatalf("delete status=%d want 204", status)
	}
}

func TestHTTPResourceDeleteInUse(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	status, _ := do(t, ts, "DELETE", "/resources/R", nil)
	if status != 409 {
		t.Fatalf("delete in-use status=%d want 409", status)
	}
}

func TestHTTPStats(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "A", Holder: "alice", TTLSeconds: 60})
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "B", Holder: "bob", TTLSeconds: 60})
	status, body := do(t, ts, "GET", "/stats", nil)
	if status != 200 {
		t.Fatalf("stats status=%d", status)
	}
	b := obj(t, body)
	if b["active_leases"].(float64) != 2 || b["total_holders"].(float64) != 2 {
		t.Fatalf("stats body=%v", body)
	}
}

func TestHTTPForceRelease(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	do(t, ts, "POST", "/acquire", acquireRequest{Resource: "R", Holder: "alice", TTLSeconds: 60})
	status, _ := do(t, ts, "DELETE", "/admin/leases/R", nil)
	if status != 204 {
		t.Fatalf("force release status=%d want 204", status)
	}
	_, body := do(t, ts, "GET", "/leases/R", nil)
	// after force release, detail returns 404
	if obj(t, body)["error"] == "" {
		t.Fatalf("expected error after force release: %v", body)
	}
}

func TestHTTPVersion(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, body := do(t, ts, "GET", "/version", nil)
	if status != 200 {
		t.Fatalf("version status=%d", status)
	}
	if obj(t, body)["name"] != "leasereg" {
		t.Fatalf("version body=%v", body)
	}
}

func TestHTTPResourceNotFound(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, _ := do(t, ts, "GET", "/resources/missing", nil)
	if status != 404 {
		t.Fatalf("status=%d want 404", status)
	}
}

func TestHTTPBatchAcquireMalformed(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	// valid JSON shape but a single item with invalid resource
	status, body := do(t, ts, "POST", "/batch/acquire", map[string]any{
		"requests": []map[string]any{{"resource": "", "holder": "a", "ttl_seconds": 60}},
	})
	if status != 200 {
		t.Fatalf("batch status=%d body=%v", status, body)
	}
	results := arr(t, body)
	r0 := results[0].(map[string]any)
	if r0["granted"] != nil {
		t.Fatalf("expected no grant: %v", r0)
	}
}

// keep json import referenced
var _ = json.Marshal
