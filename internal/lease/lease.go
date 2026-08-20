package lease

import (
	"context"
	"errors"
	"time"

	"leasereg/internal/store"
)

// Input-validation errors. These are returned before the store is touched.
var (
	// ErrEmptyResource means the resource name was empty.
	ErrEmptyResource = errors.New("resource must not be empty")
	// ErrEmptyHolder means the holder name was empty.
	ErrEmptyHolder = errors.New("holder must not be empty")
	// ErrInvalidTTL means the requested TTL was not a positive number of seconds.
	ErrInvalidTTL = errors.New("ttl_seconds must be positive")
)

// Lease is the domain view of a resource lease. Times are wall-clock moments;
// they are derived from the unix-second values persisted by the store.
type Lease struct {
	Resource     string
	Holder       string
	FencingToken int64
	AcquiredAt   time.Time
	ExpiresAt    time.Time
	TTLSeconds   int64
}

// Expired reports whether the lease has elapsed at the given instant. A lease
// whose ExpiresAt equals now is considered expired (the TTL window is
// half-open on the right).
func (l Lease) Expired(at time.Time) bool {
	return !l.ExpiresAt.After(at)
}

// Manager is the lease domain facade. It is safe for concurrent use: all
// state lives in the underlying store, which serialises writes.
type Manager struct {
	store *store.Store
	clock Clock
}

// NewManager wires a Manager to a store and clock. The caller is expected to
// perform an initial Sweep after construction to clear leases that elapsed
// while the process was down — see the RestartRecover helper.
func NewManager(s *store.Store, c Clock) *Manager {
	return &Manager{store: s, clock: c}
}

// Clock returns the manager's clock. Tests use it to advance a MockClock.
func (m *Manager) Clock() Clock { return m.clock }

// RestartRecover runs the startup sweep that clears leases which expired
// while the process was stopped. It returns the number of leases removed.
// This is the load/recovery path required after a restart: the database is
// already consistent (rows + fencing counters persisted), this just trims the
// expired tail.
func (m *Manager) RestartRecover(ctx context.Context) (int, error) {
	return m.store.Sweep(context.Background(), m.clock.Now().Unix())
}

func (m *Manager) nowSec() int64 { return m.clock.Now().Unix() }

// Acquire grants a new lease on resource to holder lasting ttlSeconds. If an
// active lease already holds the resource, it returns ErrConflict together
// with a non-nil conflict describing the current holder.
//
// The allocated fencing token is strictly greater than every token previously
// allocated for the same resource, including across release, sweep and
// process restart. This is the guarantee that lets downstream systems reject
// a stale holder that recorded an old token and later tries to renew.
func (m *Manager) Acquire(ctx context.Context, resource, holder string, ttlSeconds int64) (Lease, *Lease, error) {
	if err := validate(resource, holder, ttlSeconds); err != nil {
		return Lease{}, nil, err
	}
	now := m.clock.Now()
	token, expiresAt, conflict, err := m.store.Acquire(ctx, resource, holder, ttlSeconds, now.Unix())
	if err != nil {
		if conflict != nil {
			c := rowToLease(*conflict)
			return Lease{}, &c, store.ErrConflict
		}
		return Lease{}, nil, err
	}
	return Lease{
		Resource:     resource,
		Holder:       holder,
		FencingToken: token,
		AcquiredAt:   now,
		ExpiresAt:    time.Unix(expiresAt, 0).UTC(),
		TTLSeconds:   ttlSeconds,
	}, nil, nil
}

// Renew extends an existing, unexpired lease by ttlSeconds from now. Renewal
// is rejected once the lease has elapsed (ErrExpired) — the holder must
// re-acquire, which allocates a fresh fencing token. A stale holder that
// presents an outdated token is rejected with ErrTokenMismatch even when it
// names the correct holder, which is the fencing-token protection against a
// renewed-then-reacquired resource.
func (m *Manager) Renew(ctx context.Context, resource, holder string, fencingToken, ttlSeconds int64) (Lease, error) {
	if err := validate(resource, holder, ttlSeconds); err != nil {
		return Lease{}, err
	}
	if fencingToken <= 0 {
		return Lease{}, store.ErrTokenMismatch
	}
	now := m.clock.Now()
	acquiredAt, expiresAt, err := m.store.Renew(ctx, resource, holder, fencingToken, ttlSeconds, now.Unix())
	if err != nil {
		return Lease{}, err
	}
	return Lease{
		Resource:     resource,
		Holder:       holder,
		FencingToken: fencingToken,
		AcquiredAt:   time.Unix(acquiredAt, 0).UTC(),
		ExpiresAt:    time.Unix(expiresAt, 0).UTC(),
		TTLSeconds:   ttlSeconds,
	}, nil
}

// Release removes an unexpired lease. The holder and fencing token must match
// the current lease. Releasing an expired lease is an error (ErrExpired) — it
// has already lapsed and will be removed by Sweep.
func (m *Manager) Release(ctx context.Context, resource, holder string, fencingToken int64) error {
	if err := validate(resource, holder, 1); err != nil {
		return err
	}
	if fencingToken <= 0 {
		return store.ErrTokenMismatch
	}
	return m.store.Release(ctx, resource, holder, fencingToken, m.nowSec())
}

// Sweep removes every expired lease and returns the count. It never touches
// the fencing counters, so the next allocated token continues the global
// monotonic sequence.
func (m *Manager) Sweep(ctx context.Context) (int, error) {
	return m.store.Sweep(ctx, m.nowSec())
}

// Info returns the current lease for resource. If none exists it returns
// (zero, false, nil).
func (m *Manager) Info(ctx context.Context, resource string) (Lease, bool, error) {
	if resource == "" {
		return Lease{}, false, ErrEmptyResource
	}
	row, err := m.store.Get(ctx, resource)
	if err != nil {
		return Lease{}, false, err
	}
	if row == nil {
		return Lease{}, false, nil
	}
	return rowToLease(*row), true, nil
}

// List returns every lease (including expired ones not yet swept), ordered by
// expiry ascending.
func (m *Manager) List(ctx context.Context) ([]Lease, error) {
	rows, err := m.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Lease, len(rows))
	for i, r := range rows {
		out[i] = rowToLease(r)
	}
	return out, nil
}

// PeekFencingToken returns the next token that Acquire would allocate for
// resource. It is exposed for diagnostics and tests; it never mutates state.
func (m *Manager) PeekFencingToken(ctx context.Context, resource string) (int64, error) {
	return m.store.PeekFencingToken(ctx, resource)
}

// Transfer hands an active lease from currentHolder to newHolder, allocating a
// fresh fencing token for the new holder. The old token is invalidated by the
// new allocation, so the previous holder cannot renew afterwards. The lease
// must be unexpired and the current holder and token must match.
func (m *Manager) Transfer(ctx context.Context, resource, currentHolder string, fencingToken, ttlSeconds int64, newHolder string) (Lease, error) {
	if err := validate(resource, currentHolder, ttlSeconds); err != nil {
		return Lease{}, err
	}
	if newHolder == "" {
		return Lease{}, ErrEmptyHolder
	}
	if fencingToken <= 0 {
		return Lease{}, store.ErrTokenMismatch
	}
	now := m.clock.Now()
	row, err := m.store.Transfer(ctx, resource, currentHolder, fencingToken, ttlSeconds, now.Unix(), newHolder)
	if err != nil {
		return Lease{}, err
	}
	return rowToLease(row), nil
}

// BatchAcquire attempts to grant every lease in reqs atomically per-item
// (each item is its own transaction). It returns one result per input item,
// preserving order; a conflict on one item does not abort the others.
type BatchResult struct {
	Resource string
	Granted  *Lease
	Conflict *Lease
	Err      error
}

// BatchAcquire applies Acquire to each item independently and returns the
// per-item outcomes in input order.
func (m *Manager) BatchAcquire(ctx context.Context, reqs []AcquireItem) []BatchResult {
	out := make([]BatchResult, len(reqs))
	for i, r := range reqs {
		granted, conflict, err := m.Acquire(ctx, r.Resource, r.Holder, r.TTLSeconds)
		slot := i
		out[slot] = BatchResult{Resource: r.Resource}
		if err == nil {
			out[slot].Granted = &granted
			continue
		}
		out[slot].Err = err
		if conflict != nil {
			c := conflict
			out[slot].Conflict = c
		}
		if conflict == nil {
			break
		}
	}
	return out
}

// AcquireItem is one element of a BatchAcquire request.
type AcquireItem struct {
	Resource   string `json:"resource"`
	Holder     string `json:"holder"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

// ListExpired returns leases whose TTL has elapsed at the current instant.
func (m *Manager) ListExpired(ctx context.Context) ([]Lease, error) {
	rows, err := m.store.ListExpired(ctx, m.nowSec())
	if err != nil {
		return nil, err
	}
	out := make([]Lease, len(rows))
	for i, r := range rows {
		out[i] = rowToLease(r)
	}
	return out, nil
}

// ListByHolder returns every lease currently held by holder.
func (m *Manager) ListByHolder(ctx context.Context, holder string) ([]Lease, error) {
	if holder == "" {
		return nil, ErrEmptyHolder
	}
	rows, err := m.store.ListByHolder(ctx, holder)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	out := make([]Lease, len(rows))
	for i, r := range rows {
		out[i] = rowToLease(r)
	}
	return out, nil
}

// Summary is the domain-level statistics view.
type Summary struct {
	ActiveLeases   int `json:"active_leases"`
	ExpiredLeases  int `json:"expired_leases"`
	TotalResources int `json:"total_resources"`
	TotalHolders   int `json:"total_holders"`
}

// Stats summarises the registry at the current instant.
func (m *Manager) Stats(ctx context.Context) (Summary, error) {
	st, err := m.store.Stats(ctx, m.nowSec())
	if err != nil {
		return Summary{}, err
	}
	return Summary(st), nil
}

// ForceRelease removes a lease without checking holder or token. Admin only.
func (m *Manager) ForceRelease(ctx context.Context, resource string) error {
	if resource == "" {
		return ErrEmptyResource
	}
	return m.store.ForceRelease(ctx, resource)
}

// RegisterResource attaches a max_ttl cap and description to a resource.
func (m *Manager) RegisterResource(ctx context.Context, name, description string, maxTTL int64) error {
	return m.store.RegisterResource(ctx, name, maxTTL, description, m.nowSec())
}

// GetResource returns the metadata for a registered resource.
func (m *Manager) GetResource(ctx context.Context, name string) (store.ResourceMeta, bool, error) {
	m2, err := m.store.GetResource(ctx, name)
	if err != nil {
		if errors.Is(err, store.ErrResourceNotFound) {
			return store.ResourceMeta{}, false, nil
		}
		return store.ResourceMeta{}, false, err
	}
	return m2, true, nil
}

// ListResources returns every registered resource.
func (m *Manager) ListResources(ctx context.Context) ([]store.ResourceMeta, error) {
	return m.store.ListResources(ctx)
}

// UpdateResource changes the max_ttl/description of an existing resource.
func (m *Manager) UpdateResource(ctx context.Context, name, description string, maxTTL int64) error {
	return m.store.UpdateResource(ctx, name, maxTTL, description)
}

// DeleteResource removes a resource's metadata, refusing if it is in use.
func (m *Manager) DeleteResource(ctx context.Context, name string) error {
	return m.store.DeleteResource(ctx, name, m.nowSec())
}

func validate(resource, holder string, ttlSeconds int64) error {
	if resource == "" {
		return ErrEmptyResource
	}
	if holder == "" {
		return ErrEmptyHolder
	}
	if ttlSeconds <= 0 {
		return ErrInvalidTTL
	}
	return nil
}

func rowToLease(r store.LeaseRow) Lease {
	return Lease{
		Resource:     r.Resource,
		Holder:       r.Holder,
		FencingToken: r.FencingToken,
		AcquiredAt:   time.Unix(r.AcquiredAt, 0).UTC(),
		ExpiresAt:    time.Unix(r.ExpiresAt, 0).UTC(),
		TTLSeconds:   r.TTLSeconds,
	}
}
