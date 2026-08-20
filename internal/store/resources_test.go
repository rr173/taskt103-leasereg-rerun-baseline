package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRegisterAndGetResource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RegisterResource(ctx, "printer-1", 300, "lobby printer", 1000); err != nil {
		t.Fatalf("Register: %v", err)
	}
	m, err := s.GetResource(ctx, "printer-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.MaxTTL != 300 || m.Description != "lobby printer" || m.CreatedAt != 1000 {
		t.Fatalf("meta = %+v", m)
	}
}

func TestGetResourceNotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetResource(context.Background(), "nope")
	if !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestRegisterResourceReplaces(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RegisterResource(ctx, "R", 60, "v1", 1000); err != nil {
		t.Fatal(err)
	}
	// re-register with new max_ttl/description
	if err := s.RegisterResource(ctx, "R", 120, "v2", 2000); err != nil {
		t.Fatal(err)
	}
	m, _ := s.GetResource(ctx, "R")
	if m.MaxTTL != 120 || m.Description != "v2" {
		t.Fatalf("after re-register meta = %+v", m)
	}
}

func TestListResources(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.RegisterResource(ctx, "B", 0, "", 1000)
	s.RegisterResource(ctx, "A", 0, "", 1000)
	list, err := s.ListResources(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "A" || list[1].Name != "B" {
		t.Fatalf("list = %+v", list)
	}
}

func TestUpdateResource(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.RegisterResource(ctx, "R", 60, "old", 1000)
	if err := s.UpdateResource(ctx, "R", 200, "new"); err != nil {
		t.Fatalf("Update: %v", err)
	}
	m, _ := s.GetResource(ctx, "R")
	if m.MaxTTL != 200 || m.Description != "new" {
		t.Fatalf("after update meta = %+v", m)
	}
	// updating a missing resource fails
	if err := s.UpdateResource(ctx, "missing", 0, ""); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestDeleteResourceInUse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.RegisterResource(ctx, "R", 0, "", 1000)
	// grant an active lease
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteResource(ctx, "R", 1010); !errors.Is(err, ErrResourceInUse) {
		t.Fatalf("err = %v, want ErrResourceInUse", err)
	}
}

func TestDeleteResourceAfterExpiry(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.RegisterResource(ctx, "R", 0, "", 1000)
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 60, 1000); err != nil {
		t.Fatal(err)
	}
	// expired now; sweep then delete
	if _, err := s.Sweep(ctx, 1100); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteResource(ctx, "R", 1100); err != nil {
		t.Fatalf("Delete after sweep: %v", err)
	}
	if _, err := s.GetResource(ctx, "R"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("err = %v, want ErrResourceNotFound", err)
	}
	// deleting again -> not found
	if err := s.DeleteResource(ctx, "R", 1100); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("err = %v, want ErrResourceNotFound", err)
	}
}

func TestAcquireEnforcesMaxTTL(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.RegisterResource(ctx, "R", 100, "", 1000); err != nil {
		t.Fatal(err)
	}
	// ttl within cap -> ok
	if _, _, _, err := s.Acquire(ctx, "R", "alice", 100, 1000); err != nil {
		t.Fatalf("acquire within cap: %v", err)
	}
	// release so we can re-acquire
	if err := s.Release(ctx, "R", "alice", 1, 1001); err != nil {
		t.Fatal(err)
	}
	// ttl over cap -> rejected
	_, _, _, err := s.Acquire(ctx, "R", "bob", 101, 1002)
	if !errors.Is(err, ErrTTLExceedsMax) {
		t.Fatalf("err = %v, want ErrTTLExceedsMax", err)
	}
}

func TestAcquireNoCapWhenUnregistered(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// huge ttl, no registration -> allowed (unbounded)
	if _, _, _, err := s.Acquire(ctx, "R", "a", 999999, 1000); err != nil {
		t.Fatalf("acquire unbounded: %v", err)
	}
}

func TestTransfer(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tok, _, _, _ := s.Acquire(ctx, "R", "alice", 60, 1000)
	row, err := s.Transfer(ctx, "R", "alice", tok, 30, 1010, "bob")
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if row.Holder != "bob" || row.FencingToken != tok+1 {
		t.Fatalf("transferred row = %+v", row)
	}
	// old token (alice) cannot renew -> token mismatch (new token is tok+1)
	_, _, err = s.Renew(ctx, "R", "alice", tok, 30, 1015)
	if !errors.Is(err, ErrHolderMismatch) {
		t.Fatalf("err = %v, want ErrHolderMismatch", err)
	}
}

func TestTransferExpired(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	tok, _, _, _ := s.Acquire(ctx, "R", "alice", 60, 1000)
	_, err := s.Transfer(ctx, "R", "alice", tok, 30, 1080, "bob")
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
}

func TestListExpiredAndByHolder(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.Acquire(ctx, "A", "alice", 60, 1000)
	s.Acquire(ctx, "B", "alice", 60, 1000)
	s.Acquire(ctx, "C", "bob", 60, 1000)
	expired, err := s.ListExpired(ctx, 1100)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 3 {
		t.Fatalf("expired = %d, want 3", len(expired))
	}
	alice, err := s.ListByHolder(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 2 {
		t.Fatalf("alice leases = %d, want 2", len(alice))
	}
}

func TestStats(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.Acquire(ctx, "A", "alice", 60, 1000)
	s.Acquire(ctx, "B", "bob", 60, 1000)
	st, err := s.Stats(ctx, 1010)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveLeases != 2 || st.ExpiredLeases != 0 || st.TotalResources != 2 || st.TotalHolders != 2 {
		t.Fatalf("stats = %+v", st)
	}
	// advance past expiry
	st2, _ := s.Stats(ctx, 1100)
	if st2.ActiveLeases != 0 || st2.ExpiredLeases != 2 {
		t.Fatalf("stats2 = %+v", st2)
	}
}

func TestForceRelease(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	s.Acquire(ctx, "R", "alice", 60, 1000)
	if err := s.ForceRelease(ctx, "R"); err != nil {
		t.Fatalf("ForceRelease: %v", err)
	}
	if err := s.ForceRelease(ctx, "R"); !errors.Is(err, ErrNoLease) {
		t.Fatalf("err = %v, want ErrNoLease", err)
	}
}

func TestResourcesPersistAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "res.db")
	ctx := context.Background()
	s1, _ := Open(path)
	s1.RegisterResource(ctx, "R", 100, "desc", 1000)
	s1.Close()
	s2, _ := Open(path)
	defer s2.Close()
	m, err := s2.GetResource(ctx, "R")
	if err != nil {
		t.Fatalf("Get after reopen: %v", err)
	}
	if m.MaxTTL != 100 || m.Description != "desc" {
		t.Fatalf("meta after reopen = %+v", m)
	}
}
