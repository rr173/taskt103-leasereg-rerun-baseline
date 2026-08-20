package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"database/sql"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenMigrate(t *testing.T) {
	s := newTestStore(t)
	var name string
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='leases'`).Scan(&name); err != nil {
		t.Fatalf("leases table missing: %v", err)
	}
	if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='fencing_counters'`).Scan(&name); err != nil {
		t.Fatalf("fencing_counters table missing: %v", err)
	}
}

func TestAcquireGrantsToken1(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	token, expiresAt, conflict, err := s.Acquire(ctx, "R", "alice", 60, 1000)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if token != 1 {
		t.Fatalf("first token = %d, want 1", token)
	}
	if expiresAt != 1060 {
		t.Fatalf("expiresAt = %d, want 1060", expiresAt)
	}
	if conflict != nil {
		t.Fatalf("unexpected conflict %+v", conflict)
	}
}

func TestAcquireConflictActiveLease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	token, _, conflict, err := s.Acquire(ctx, "R", "bob", 60, 1010)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if token != 0 {
		t.Fatalf("token = %d, want 0 on conflict", token)
	}
	if conflict == nil || conflict.Holder != "alice" || conflict.FencingToken != 1 {
		t.Fatalf("conflict = %+v", conflict)
	}
}

func TestAcquireAfterExpiredReplacesAndIncrements(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	// now=1080 is past expiry (1060): acquire must succeed with token 2
	token, expiresAt, conflict, err := s.Acquire(ctx, "R", "bob", 30, 1080)
	if err != nil {
		t.Fatalf("Acquire after expiry: %v", err)
	}
	if token != 2 {
		t.Fatalf("token = %d, want 2", token)
	}
	if conflict != nil {
		t.Fatalf("unexpected conflict %+v", conflict)
	}
	if expiresAt != 1110 {
		t.Fatalf("expiresAt = %d, want 1110", expiresAt)
	}
}

func TestRenewValid(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	acq, exp, err := s.Renew(ctx, "R", "alice", 1, 50, 1020)
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if acq != 1000 {
		t.Fatalf("acquiredAt = %d, want 1000", acq)
	}
	if exp != 1070 {
		t.Fatalf("expiresAt = %d, want 1070", exp)
	}
}

func TestRenewExpiredFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Renew(ctx, "R", "alice", 1, 50, 1080)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestRenewTokenMismatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Renew(ctx, "R", "alice", 999, 50, 1010)
	if !errors.Is(err, ErrTokenMismatch) {
		t.Fatalf("err = %v, want ErrTokenMismatch", err)
	}
}

func TestRenewHolderMismatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Renew(ctx, "R", "bob", 1, 50, 1010)
	if !errors.Is(err, ErrHolderMismatch) {
		t.Fatalf("err = %v, want ErrHolderMismatch", err)
	}
}

func TestRenewNoLease(t *testing.T) {
	s := newTestStore(t)
	_, _, err := s.Renew(context.Background(), "missing", "alice", 1, 50, 1000)
	if !errors.Is(err, ErrNoLease) {
		t.Fatalf("err = %v, want ErrNoLease", err)
	}
}

func TestReleaseAndNoLease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "R", "alice", 1, 1010); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := s.Release(ctx, "R", "alice", 1, 1010); !errors.Is(err, ErrNoLease) {
		t.Fatalf("second Release err = %v, want ErrNoLease", err)
	}
}

func TestReleaseExpiredFails(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.Release(ctx, "R", "alice", 1, 1080); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestSweep(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "A", "h", 60, 1000); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Acquire(ctx, "B", "h", 60, 1000); err != nil {
		t.Fatal(err)
	}
	// at now=1100 both expired
	n, err := s.Sweep(ctx, 1100)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if n != 2 {
		t.Fatalf("swept = %d, want 2", n)
	}
	// sweeping again removes nothing
	n2, _ := s.Sweep(ctx, 1100)
	if n2 != 0 {
		t.Fatalf("second sweep = %d, want 0", n2)
	}
}

func TestFencingCounterMonotonicAcrossRelease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t1, _, _, _ := s.Acquire(ctx, "R", "a", 60, 1000)
	_ = s.Release(ctx, "R", "a", 1, 1001)
	t2, _, _, _ := s.Acquire(ctx, "R", "b", 60, 1002)
	if t1 != 1 || t2 != 2 {
		t.Fatalf("tokens = %d,%d, want 1,2", t1, t2)
	}
	// peek should report 3 (next to allocate)
	peek, err := s.PeekFencingToken(ctx, "R")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if peek != 3 {
		t.Fatalf("peek = %d, want 3", peek)
	}
}

func TestRecoveryAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.db")
	ctx := context.Background()

	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// grant an unexpired lease and an expired one
	tok, _, _, err := s1.Acquire(ctx, "live", "alice", 600, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s1.Acquire(ctx, "dead", "bob", 10, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	// reopen at now=1015: "dead" expired (1010), "live" still valid (1600)
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	n, err := s2.Sweep(ctx, 1015)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("restart sweep = %d, want 1", n)
	}
	live, err := s2.Get(ctx, "live")
	if err != nil {
		t.Fatal(err)
	}
	if live == nil || live.Holder != "alice" || live.FencingToken != tok {
		t.Fatalf("live lease not recovered: %+v", live)
	}
	// fencing counter for "live" persisted across reopen: the next token
	// allocated for the SAME resource is tok+1 (per-resource monotonicity).
	peek, err := s2.PeekFencingToken(ctx, "live")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}
	if peek != tok+1 {
		t.Fatalf("post-restart next token = %d, want %d", peek, tok+1)
	}
}

func TestFencingPerResource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	t1, _, _, _ := s.Acquire(ctx, "A", "h", 60, 1000)
	t2, _, _, _ := s.Acquire(ctx, "B", "h", 60, 1000) // different resource
	if t1 != 1 || t2 != 1 {
		t.Fatalf("per-resource first tokens = %d,%d, want 1,1", t1, t2)
	}
	// releasing and re-acquiring the SAME resource advances only its counter
	_ = s.Release(ctx, "A", "h", 1, 1001)
	t3, _, _, _ := s.Acquire(ctx, "A", "h", 60, 1002)
	if t3 != 2 {
		t.Fatalf("A second token = %d, want 2", t3)
	}
	// B is untouched: its next token is still 2
	peek, _ := s.PeekFencingToken(ctx, "B")
	if peek != 2 {
		t.Fatalf("B next token = %d, want 2", peek)
	}
}

func TestGetList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if _, _, _, err := s.Acquire(ctx, "A", "h1", 60, 1000); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Acquire(ctx, "B", "h2", 30, 1000); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, "A")
	if err != nil || got == nil || got.Holder != "h1" {
		t.Fatalf("Get A = %+v err=%v", got, err)
	}
	none, err := s.Get(ctx, "missing")
	if err != nil || none != nil {
		t.Fatalf("Get missing = %+v err=%v", none, err)
	}
	all, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List len = %d, want 2", len(all))
	}
	// ordered by expiry asc: B (1030) before A (1060)
	if all[0].Resource != "B" || all[1].Resource != "A" {
		t.Fatalf("List order = %s,%s", all[0].Resource, all[1].Resource)
	}
}

func TestPing(t *testing.T) {
	s := newTestStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

// silence unused import if database/sql is only referenced via sql.ErrNoRows in other files
var _ = sql.ErrNoRows
