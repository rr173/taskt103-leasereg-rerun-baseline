package lease

import (
	"context"
	"testing"
	"time"
)

func TestInfoHidesExpiredLease(t *testing.T) {
	m, _, clock, _ := newManager(t, time.Unix(1000, 0))
	if _, _, err := m.Acquire(context.Background(), "R", "alice", 10); err != nil {
		t.Fatal(err)
	}
	clock.Advance(20 * time.Second)
	_, ok, err := m.Info(context.Background(), "R")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("Info returned an expired lease as current")
	}
}
