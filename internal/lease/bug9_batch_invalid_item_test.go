package lease

import (
	"context"
	"testing"
	"time"
)

func TestBatchAcquireContinuesAfterInvalidItem(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	results := m.BatchAcquire(context.Background(), []AcquireItem{
		{Resource: "", Holder: "alice", TTLSeconds: 60},
		{Resource: "R", Holder: "bob", TTLSeconds: 60},
	})
	if len(results) != 2 || results[0].Err == nil {
		t.Fatalf("results=%+v, want first item validation error", results)
	}
	if results[1].Granted == nil || results[1].Granted.Resource != "R" {
		t.Fatalf("results=%+v, want second item granted", results)
	}
}
