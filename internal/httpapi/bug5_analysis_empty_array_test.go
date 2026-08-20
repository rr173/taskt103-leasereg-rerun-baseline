package httpapi

import (
	"testing"
	"time"
)

func TestAnalysisReturnsEmptyAuditArray(t *testing.T) {
	ts, _ := newTestServer(t, time.Unix(1000, 0))
	status, body := do(t, ts, "GET", "/analysis", nil)
	if status != 200 {
		t.Fatalf("status=%d body=%v", status, body)
	}
	audit, ok := obj(t, body)["audit_findings"].([]any)
	if !ok || audit == nil {
		t.Fatalf("audit_findings=%v, want JSON empty array", obj(t, body)["audit_findings"])
	}
	if len(audit) != 0 {
		t.Fatalf("audit_findings=%v, want empty", audit)
	}
}
