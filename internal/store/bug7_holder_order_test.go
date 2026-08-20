package store

import (
	"context"
	"testing"
)

func TestListByHolderOrdersByExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "a", "alice", 100, 1000); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Acquire(ctx, "z", "alice", 10, 1000); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListByHolder(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Resource != "z" || rows[1].Resource != "a" {
		t.Fatalf("rows=%+v, want z then a by expiry", rows)
	}
}
