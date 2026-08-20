package lease

import (
	"context"
	"errors"
	"testing"
	"time"

	"leasereg/internal/store"
)

func TestManagerTransfer(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, _, err := m.Acquire(ctx, "R", "alice", 60); err != nil {
		t.Fatal(err)
	}
	l, err := m.Transfer(ctx, "R", "alice", 1, 60, "bob")
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if l.Holder != "bob" || l.FencingToken != 2 {
		t.Fatalf("transferred lease = %+v", l)
	}
	// alice's old token no longer works (holder mismatch)
	if _, err := m.Renew(ctx, "R", "alice", 1, 60); !errors.Is(err, store.ErrHolderMismatch) {
		t.Fatalf("err = %v, want ErrHolderMismatch", err)
	}
}

func TestManagerTransferValidation(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if _, err := m.Transfer(ctx, "", "a", 1, 60, "b"); !errors.Is(err, ErrEmptyResource) {
		t.Fatalf("err=%v want ErrEmptyResource", err)
	}
	if _, err := m.Transfer(ctx, "R", "", 1, 60, "b"); !errors.Is(err, ErrEmptyHolder) {
		t.Fatalf("err=%v want ErrEmptyHolder", err)
	}
	if _, err := m.Transfer(ctx, "R", "a", 1, 60, ""); !errors.Is(err, ErrEmptyHolder) {
		t.Fatalf("err=%v want ErrEmptyHolder", err)
	}
	if _, err := m.Transfer(ctx, "R", "a", 0, 60, "b"); !errors.Is(err, store.ErrTokenMismatch) {
		t.Fatalf("err=%v want ErrTokenMismatch", err)
	}
}

func TestManagerBatchAcquire(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	// pre-acquire R2 so it conflicts
	if _, _, err := m.Acquire(ctx, "R2", "carol", 60); err != nil {
		t.Fatal(err)
	}
	results := m.BatchAcquire(ctx, []AcquireItem{
		{Resource: "R1", Holder: "alice", TTLSeconds: 60},
		{Resource: "R2", Holder: "bob", TTLSeconds: 60}, // conflict
		{Resource: "R3", Holder: "dave", TTLSeconds: 60},
	})
	if len(results) != 3 {
		t.Fatalf("len=%d want 3", len(results))
	}
	if results[0].Granted == nil || results[0].Granted.FencingToken != 1 {
		t.Fatalf("results[0] = %+v", results[0])
	}
	if results[1].Granted != nil || results[1].Conflict == nil || results[1].Conflict.Holder != "carol" {
		t.Fatalf("results[1] = %+v", results[1])
	}
	if results[2].Granted == nil || results[2].Granted.FencingToken != 1 {
		t.Fatalf("results[2] = %+v", results[2])
	}
}

func TestManagerBatchAcquireValidation(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	results := m.BatchAcquire(ctx, []AcquireItem{
		{Resource: "", Holder: "a", TTLSeconds: 60}, // invalid
	})
	if results[0].Err == nil {
		t.Fatalf("expected validation error, got %+v", results[0])
	}
}

func TestManagerListExpiredAndByHolder(t *testing.T) {
	m, _, clk, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	m.Acquire(ctx, "A", "alice", 60)
	m.Acquire(ctx, "B", "alice", 60)
	m.Acquire(ctx, "C", "bob", 60)
	clk.Advance(120 * time.Second)
	expired, err := m.ListExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 3 {
		t.Fatalf("expired = %d want 3", len(expired))
	}
	alice, err := m.ListByHolder(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 2 {
		t.Fatalf("alice = %d want 2", len(alice))
	}
}

func TestManagerStats(t *testing.T) {
	m, _, clk, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	m.Acquire(ctx, "A", "alice", 60)
	m.Acquire(ctx, "B", "bob", 60)
	st, err := m.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.ActiveLeases != 2 || st.ExpiredLeases != 0 || st.TotalResources != 2 || st.TotalHolders != 2 {
		t.Fatalf("stats = %+v", st)
	}
	clk.Advance(120 * time.Second)
	st2, _ := m.Stats(ctx)
	if st2.ActiveLeases != 0 || st2.ExpiredLeases != 2 {
		t.Fatalf("stats2 = %+v", st2)
	}
}

func TestManagerResourceMetadata(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if err := m.RegisterResource(ctx, "printer-1", "lobby", 100); err != nil {
		t.Fatal(err)
	}
	m2, ok, err := m.GetResource(ctx, "printer-1")
	if err != nil || !ok || m2.MaxTTL != 100 {
		t.Fatalf("get resource: m=%+v ok=%v err=%v", m2, ok, err)
	}
	list, _ := m.ListResources(ctx)
	if len(list) != 1 {
		t.Fatalf("list len=%d want 1", len(list))
	}
	if err := m.UpdateResource(ctx, "printer-1", "updated", 200); err != nil {
		t.Fatal(err)
	}
	m3, _, _ := m.GetResource(ctx, "printer-1")
	if m3.MaxTTL != 200 {
		t.Fatalf("after update max=%d want 200", m3.MaxTTL)
	}
	if err := m.DeleteResource(ctx, "printer-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, ok, _ = m.GetResource(ctx, "printer-1")
	if ok {
		t.Fatal("resource should be deleted")
	}
}

func TestManagerAcquireEnforcesMaxTTL(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	if err := m.RegisterResource(ctx, "R", "", 100); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Acquire(ctx, "R", "a", 100); err != nil {
		t.Fatalf("acquire within cap: %v", err)
	}
	// release to re-acquire
	if err := m.Release(ctx, "R", "a", 1); err != nil {
		t.Fatal(err)
	}
	_, _, err := m.Acquire(ctx, "R", "b", 101)
	if !errors.Is(err, store.ErrTTLExceedsMax) {
		t.Fatalf("err = %v, want ErrTTLExceedsMax", err)
	}
}

func TestManagerDeleteResourceInUse(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	m.RegisterResource(ctx, "R", "", 0)
	m.Acquire(ctx, "R", "alice", 60)
	if err := m.DeleteResource(ctx, "R"); !errors.Is(err, store.ErrResourceInUse) {
		t.Fatalf("err = %v, want ErrResourceInUse", err)
	}
}

func TestManagerForceRelease(t *testing.T) {
	m, _, _, _ := newManager(t, time.Unix(1000, 0))
	ctx := context.Background()
	m.Acquire(ctx, "R", "alice", 60)
	if err := m.ForceRelease(ctx, "R"); err != nil {
		t.Fatalf("ForceRelease: %v", err)
	}
	_, ok, _ := m.Info(ctx, "R")
	if ok {
		t.Fatal("lease should be gone after force release")
	}
}
