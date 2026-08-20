// Package lease implements the resource-lease domain on top of a persistent
// store. It owns the clock abstraction (so tests can advance time without
// sleeping) and converts the store's row-oriented representation into domain
// Lease values.
//
// The package deliberately keeps no in-memory cache of leases: the SQLite
// database is the single source of truth, which is what makes a process
// restart transparent — the next process opens the same file and immediately
// observes every unexpired lease together with the per-resource fencing
// counter sequence.
package lease

import (
	"sync"
	"time"
)

// Clock abstracts the current time so the Manager can be tested
// deterministically. Production code uses RealClock; tests use MockClock to
// advance time past a TTL without sleeping.
type Clock interface {
	Now() time.Time
}

// RealClock returns the wall-clock time.
type RealClock struct{}

// Now returns time.Now.
func (RealClock) Now() time.Time { return time.Now() }

// MockClock is a controllable Clock. It is safe for concurrent use because
// the background sweeper and HTTP handlers may both read the time.
type MockClock struct {
	mu sync.Mutex
	t  time.Time
}

// NewMockClock returns a MockClock frozen at at.
func NewMockClock(at time.Time) *MockClock {
	return &MockClock{t: at}
}

// Now returns the frozen time.
func (c *MockClock) Now() time.Time {
	return c.t
}

// Advance moves the frozen time forward by d. Negative durations move it back,
// which is occasionally useful for boundary tests.
func (c *MockClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// Set replaces the frozen time with t.
func (c *MockClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = t
}
