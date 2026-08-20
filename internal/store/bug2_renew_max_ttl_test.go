package store

import (
	"context"
	"errors"
	"testing"
)

func TestRenewEnforcesResourceMaxTTL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RegisterResource(ctx, "R", 60, "", 1000); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := s.Acquire(ctx, "R", "alice", 30, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Renew(ctx, "R", "alice", token, 61, 1010); !errors.Is(err, ErrTTLExceedsMax) {
		t.Fatalf("Renew err = %v, want ErrTTLExceedsMax", err)
	}
	row, err := s.Get(ctx, "R")
	if err != nil || row == nil || row.ExpiresAt != 1030 {
		t.Fatalf("lease after rejected renew = %+v err=%v", row, err)
	}
}
