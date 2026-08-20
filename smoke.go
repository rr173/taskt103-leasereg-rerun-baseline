// Smoke test: run with `leasereg --smoke-test`. It exercises the full
// acquire/renew/release/conflict/expiry/fencing/restart cycle against a
// temporary SQLite file, prints SMOKE_OK and exits 0. No external services.
//
// The fencing-token counter is per-resource and persisted in the database, so
// the same resource's token sequence (1, 2, 3, 4, 5) continues unbroken
// across release, sweep and process restart. This is the core guarantee the
// smoke test verifies.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"leasereg/internal/lease"
	"leasereg/internal/store"
)

func runSmoke() int {
	dir, err := os.MkdirTemp("", "leasereg-smoke-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "mkdirtemp:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	dbPath := filepath.Join(dir, "smoke.db")
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	check := func(step string, ok bool, got any) {
		if !ok {
			fmt.Fprintf(os.Stderr, "SMOKE_FAIL at %s: got %v\n", step, got)
			os.Exit(1)
		}
	}
	mustNoErr := func(step string, err error) {
		if err != nil {
			fmt.Fprintf(os.Stderr, "SMOKE_FAIL at %s: %v\n", step, err)
			os.Exit(1)
		}
	}

	// --- Phase 1: acquire / renew / conflict ----------------------------
	st, err := store.Open(dbPath)
	mustNoErr("open", err)
	clk := lease.NewMockClock(base)
	mgr := lease.NewManager(st, clk)

	l1, conflict, err := mgr.Acquire(ctx, "printer-1", "alice", 60)
	mustNoErr("acquire printer-1", err)
	check("acquire token=1", l1.FencingToken == 1, l1.FencingToken)
	check("acquire holder", l1.Holder == "alice", l1.Holder)
	check("no conflict on fresh acquire", conflict == nil, conflict)

	_, conflict, err = mgr.Acquire(ctx, "printer-1", "bob", 60)
	if !errors.Is(err, store.ErrConflict) {
		fmt.Fprintf(os.Stderr, "SMOKE_FAIL: expected conflict, got err=%v\n", err)
		os.Exit(1)
	}
	check("conflict holder alice", conflict != nil && conflict.Holder == "alice", conflict)

	clk.Advance(10 * time.Second)
	rn, err := mgr.Renew(ctx, "printer-1", "alice", 1, 60)
	mustNoErr("renew alice", err)
	check("renew keeps token", rn.FencingToken == 1, rn.FencingToken)
	check("renew extends expiry", rn.ExpiresAt.Equal(base.Add(70*time.Second)), rn.ExpiresAt)

	// --- Phase 2: expiry forces re-acquire with a new token ------------
	clk.Advance(120 * time.Second) // now well past expiry
	_, err = mgr.Renew(ctx, "printer-1", "alice", 1, 60)
	check("renew after expiry fails", errors.Is(err, store.ErrExpired), err)

	// same holder re-acquires the SAME resource -> NEW fencing token (not 1)
	l3, _, err := mgr.Acquire(ctx, "printer-1", "alice", 60)
	mustNoErr("reacquire alice", err)
	check("reacquire token=2", l3.FencingToken == 2, l3.FencingToken)

	// stale token (1) is rejected even for the right holder -> fencing protection
	_, err = mgr.Renew(ctx, "printer-1", "alice", 1, 60)
	check("stale token rejected", errors.Is(err, store.ErrTokenMismatch), err)

	// wrong holder rejected
	_, err = mgr.Renew(ctx, "printer-1", "carol", 2, 60)
	check("wrong holder rejected", errors.Is(err, store.ErrHolderMismatch), err)

	// release with correct token
	err = mgr.Release(ctx, "printer-1", "alice", 2)
	mustNoErr("release alice", err)

	// --- Phase 3: counter survives release + sweep ---------------------
	l4, _, err := mgr.Acquire(ctx, "printer-1", "dave", 60)
	mustNoErr("acquire dave", err)
	check("token continues after release (3)", l4.FencingToken == 3, l4.FencingToken)

	clk.Advance(120 * time.Second) // dave's lease expires
	n, err := mgr.Sweep(ctx)
	mustNoErr("sweep", err)
	check("swept 1", n == 1, n)

	// --- Phase 4: restart recovery, counter continues -------------------
	mustNoErr("close store", st.Close())

	// "restart": reopen the SAME file. The per-resource fencing counter for
	// printer-1 persisted (next would be 4); a fresh acquire of the SAME
	// resource must therefore get token 4, NOT 1.
	st2, err := store.Open(dbPath)
	mustNoErr("reopen", err)
	defer st2.Close()
	clk2 := lease.NewMockClock(base.Add(260 * time.Second))
	mgr2 := lease.NewManager(st2, clk2)

	rn0, err := mgr2.RestartRecover(ctx)
	mustNoErr("restart recover", err)
	check("restart sweep 0", rn0 == 0, rn0)

	l5, _, err := mgr2.Acquire(ctx, "printer-1", "eve", 60)
	mustNoErr("acquire printer-1 after restart", err)
	check("fencing survives restart (4)", l5.FencingToken == 4, l5.FencingToken)

	// --- Phase 5: expired-on-restart is swept, counter continues -------
	// eve's lease expires at base+320. Restart past that -> swept on recovery.
	mustNoErr("close store2", st2.Close())
	st3, err := store.Open(dbPath)
	mustNoErr("reopen3", err)
	defer st3.Close()
	clk3 := lease.NewMockClock(base.Add(500 * time.Second))
	mgr3 := lease.NewManager(st3, clk3)
	swept, err := mgr3.RestartRecover(ctx)
	mustNoErr("restart recover3", err)
	check("expired swept on restart", swept == 1, swept)

	// the expired resource is free again, and the per-resource token continues
	l6, _, err := mgr3.Acquire(ctx, "printer-1", "gina", 60)
	mustNoErr("reacquire printer-1 after restart sweep", err)
	check("token after restart sweep (5)", l6.FencingToken == 5, l6.FencingToken)

	fmt.Println("SMOKE_OK")
	return 0
}
