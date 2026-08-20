package store

import (
	"context"
	"testing"
)

func TestTransferPreservesAcquiredAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	token, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000)
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.Transfer(ctx, "R", "alice", token, 30, 1010, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if row.AcquiredAt != 1000 {
		t.Fatalf("acquired_at=%d, want original acquisition time 1000", row.AcquiredAt)
	}
}
