package lease

import (
	"context"
	"testing"
	"time"
)

func TestStatsExcludesExpiredLeaseFromHolders(t *testing.T) {
	m, _, clock, _ := newManager(t, time.Unix(1000, 0))
	if _, _, err := m.Acquire(context.Background(), "R", "alice", 60); err != nil {
		t.Fatal(err)
	}
	clock.Advance(120 * time.Second)
	stats, err := m.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveLeases != 0 || stats.ExpiredLeases != 1 || stats.TotalHolders != 0 {
		t.Fatalf("stats = %+v, want no holder for expired lease", stats)
	}
}
