package store

import (
	"context"
	"testing"
)

func TestResourceDeletePreservesFencingCounter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RegisterResource(ctx, "R", 0, "", 1000); err != nil {
		t.Fatal(err)
	}
	token, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "R", "alice", token, 1010); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteResource(ctx, "R", 1010); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterResource(ctx, "R", 0, "re-registered", 1020); err != nil {
		t.Fatal(err)
	}
	next, _, _, err := s.Acquire(ctx, "R", "bob", 60, 1020)
	if err != nil {
		t.Fatal(err)
	}
	if next != 2 {
		t.Fatalf("token after resource re-registration=%d, want 2", next)
	}
}
