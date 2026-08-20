package lease

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"leasereg/internal/store"
)

func newManager(t *testing.T, at time.Time) (*Manager, *store.Store, *MockClock, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lease.db")
	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	clk := NewMockClock(at)
	m := NewManager(s, clk)
	t.Cleanup(func() { s.Close() })
	return m, s, clk, path
}

func TestManagerAcquireFlow(t *testing.T) {
	m, _, clk, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()

	l, conflict, err := m.Acquire(ctx, "R", "alice", 60)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l.FencingToken != 1 || conflict != nil {
		t.Fatalf("got token=%d conflict=%v", l.FencingToken, conflict)
	}
	if !l.ExpiresAt.Equal(time.Unix(1060, 0)) {
		t.Fatalf("expiresAt = %v", l.ExpiresAt)
	}
	// expired flag computed against now
	if l.Expired(clk.Now()) {
		t.Fatal("lease should not be expired")
	}
}

func TestManagerAcquireConflict(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, "R", "alice", 60); err != nil {
		t.Fatal(err)
	}
	_, conflict, err := m.Acquire(ctx, "R", "bob", 60)
	if !errors.Is(err, store.ErrConflict) {
		t.Fatalf("err=%v want ErrConflict", err)
	}
	if conflict == nil || conflict.Holder != "alice" {
		t.Fatalf("conflict=%+v", conflict)
	}
}

func TestManagerRenewAfterExpiryFailsAndReacquire(t *testing.T) {
	m, _, clk, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, "R", "alice", 60); err != nil {
		t.Fatal(err)
	}
	clk.Advance(120 * time.Second) // past expiry
	if _, err := m.Renew(ctx, "R", "alice", 1, 60); !errors.Is(err, store.ErrExpired) {
		t.Fatalf("err=%v want ErrExpired", err)
	}
	// re-acquire by the SAME holder yields a NEW token (the fencing guarantee)
	l, _, err := m.Acquire(ctx, "R", "alice", 60)
	if err != nil {
		t.Fatal(err)
	}
	if l.FencingToken != 2 {
		t.Fatalf("reacquire token=%d want 2", l.FencingToken)
	}
	// stale token (1) rejected even though holder matches
	if _, err := m.Renew(ctx, "R", "alice", 1, 60); !errors.Is(err, store.ErrTokenMismatch) {
		t.Fatalf("err=%v want ErrTokenMismatch", err)
	}
}

func TestManagerFencingSurvivesReleaseAndSweep(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, "R", "a", 60); err != nil {
		t.Fatal(err)
	}
	if err := m.Release(ctx, "R", "a", 1); err != nil {
		t.Fatal(err)
	}
	l, _, err := m.Acquire(ctx, "R", "b", 60)
	if err != nil {
		t.Fatal(err)
	}
	if l.FencingToken != 2 {
		t.Fatalf("token=%d want 2", l.FencingToken)
	}
}

func TestManagerFencingAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r.db")
	ctx := context.Background()

	s1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clk1 := NewMockClock(time.Unix(1000, 0))
	m1 := NewManager(s1, clk1)
	if _, _, err := m1.Acquire(ctx, "R", "alice", 600); err != nil { // token 1, unexpired
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// restart: reopen same file; lease survives, counter continues
	s2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	clk2 := NewMockClock(time.Unix(1010, 0))
	m2 := NewManager(s2, clk2)
	n, err := m2.RestartRecover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("restart sweep=%d want 0", n)
	}
	info, ok, err := m2.Info(ctx, "R")
	if err != nil || !ok || info.Holder != "alice" || info.FencingToken != 1 {
		t.Fatalf("recovered info=%+v ok=%v err=%v", info, ok, err)
	}
	// the per-resource fencing counter persisted across restart: the next
	// token that would be allocated for "R" is 2 (token 1 was allocated, the
	// counter was not reset by the restart).
	peek, err := m2.PeekFencingToken(ctx, "R")
	if err != nil {
		t.Fatal(err)
	}
	if peek != 2 {
		t.Fatalf("post-restart next token=%d want 2", peek)
	}
}

func TestManagerExpiredSweptOnRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "r2.db")
	ctx := context.Background()

	s1, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	clk1 := NewMockClock(time.Unix(1000, 0))
	m1 := NewManager(s1, clk1)
	if _, _, err := m1.Acquire(ctx, "R", "alice", 60); err != nil { // expires 1060
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// restart after expiry
	s2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	clk2 := NewMockClock(time.Unix(2000, 0))
	m2 := NewManager(s2, clk2)
	n, err := m2.RestartRecover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("restart sweep=%d want 1", n)
	}
	_, ok, err := m2.Info(ctx, "R")
	if err != nil || ok {
		t.Fatalf("expired lease should be gone: ok=%v err=%v", ok, err)
	}
	// token continues (was 1, next is 2)
	l, _, err := m2.Acquire(ctx, "R", "bob", 60)
	if err != nil {
		t.Fatal(err)
	}
	if l.FencingToken != 2 {
		t.Fatalf("token=%d want 2", l.FencingToken)
	}
}

func TestManagerValidation(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, "", "a", 60); !errors.Is(err, ErrEmptyResource) {
		t.Fatalf("err=%v want ErrEmptyResource", err)
	}
	if _, _, err := m.Acquire(ctx, "R", "", 60); !errors.Is(err, ErrEmptyHolder) {
		t.Fatalf("err=%v want ErrEmptyHolder", err)
	}
	if _, _, err := m.Acquire(ctx, "R", "a", 0); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("err=%v want ErrInvalidTTL", err)
	}
	if _, _, err := m.Acquire(ctx, "R", "a", -1); !errors.Is(err, ErrInvalidTTL) {
		t.Fatalf("err=%v want ErrInvalidTTL", err)
	}
	if _, err := m.Renew(ctx, "R", "a", 0, 60); !errors.Is(err, store.ErrTokenMismatch) {
		t.Fatalf("err=%v want ErrTokenMismatch", err)
	}
	if err := m.Release(ctx, "R", "a", -1); !errors.Is(err, store.ErrTokenMismatch) {
		t.Fatalf("err=%v want ErrTokenMismatch", err)
	}
}

func TestManagerSweepAndList(t *testing.T) {
	m, _, clk, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, "A", "h", 60); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Acquire(ctx, "B", "h", 30); err != nil {
		t.Fatal(err)
	}
	clk.Advance(100 * time.Second)
	n, err := m.Sweep(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("swept=%d want 2", n)
	}
	all, err := m.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Fatalf("list len=%d want 0", len(all))
	}
}

func TestManagerInfoMissing(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	_, ok, err := m.Info(context.Background(), "none")
	if err != nil || ok {
		t.Fatalf("info missing: ok=%v err=%v", ok, err)
	}
}

func TestLeaseExpired(t *testing.T) {
	l := Lease{ExpiresAt: time.Unix(1000, 0)}
	if !l.Expired(time.Unix(1000, 0)) {
		t.Fatal("expiresAt==now should be expired")
	}
	if l.Expired(time.Unix(999, 0)) {
		t.Fatal("before expiry should not be expired")
	}
}
