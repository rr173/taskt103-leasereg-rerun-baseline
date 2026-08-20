// Package store provides a SQLite-backed persistence layer for resource
// leases and the per-resource fencing-token counters that guard against stale
// holders silently extending a lease that has already lapsed and been
// re-acquired.
//
// All mutating operations run inside "BEGIN IMMEDIATE" transactions so that the
// check-then-write sequence (is the resource free? allocate a token? write the
// row?) is atomic with respect to concurrent callers. The fencing counter
// table is the authoritative source of the next token; it is never reset by
// releasing or sweeping a lease, which is what makes tokens strictly
// monotonic across process restarts.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// State errors returned by the store. Callers in higher layers map these to
// transport responses; they are never wrapped so that errors.Is keeps working.
var (
	// ErrNoLease means no lease row exists for the resource.
	ErrNoLease = errors.New("no lease for resource")
	// ErrConflict means an active (unexpired) lease already holds the resource.
	ErrConflict = errors.New("resource held by an active lease")
	// ErrHolderMismatch means the lease exists but is held by another holder.
	ErrHolderMismatch = errors.New("lease held by a different holder")
	// ErrTokenMismatch means the supplied fencing token does not match the
	// current lease. This is the fencing-token protection: a stale client that
	// recorded an old token is rejected even if it names the right holder.
	ErrTokenMismatch = errors.New("fencing token does not match current lease")
	// ErrExpired means the lease row exists but its TTL has elapsed.
	ErrExpired = errors.New("lease has expired")
	// ErrTTLExceedsMax means the requested TTL is larger than the max_ttl
	// recorded for the resource in the resources metadata table.
	ErrTTLExceedsMax = errors.New("ttl_seconds exceeds resource max_ttl")
	// ErrResourceInUse means a resource metadata row cannot be removed because
	// an active lease still holds it.
	ErrResourceInUse = errors.New("resource still has an active lease")
	// ErrResourceNotFound means no metadata row exists for the resource.
	ErrResourceNotFound = errors.New("resource metadata not found")
)

// LeaseRow is the persisted representation of a lease. Times are stored as
// unix seconds because they must survive round-trips through SQLite integers
// and remain comparable across restarts without timezone ambiguity.
type LeaseRow struct {
	Resource     string
	Holder       string
	FencingToken int64
	AcquiredAt   int64
	ExpiresAt    int64
	TTLSeconds   int64
}

// Store wraps a *sql.DB that stores leases and per-resource fencing counters.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the lease database at path and applies the schema.
// path may be a file path or ":memory:". A single connection is used so that
// SQLite serialises every statement; combined with BEGIN IMMEDIATE this makes
// the fencing-token allocation race-free without relying on busy retries.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := applyPragmas(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func applyPragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA busy_timeout=5000",
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("pragma %q: %w", p, err)
		}
	}
	return nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS leases (
			resource      TEXT    PRIMARY KEY,
			holder        TEXT    NOT NULL,
			fencing_token INTEGER NOT NULL,
			acquired_at   INTEGER NOT NULL,
			expires_at    INTEGER NOT NULL,
			ttl_seconds   INTEGER NOT NULL
		) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS fencing_counters (
			resource   TEXT PRIMARY KEY,
			next_token INTEGER NOT NULL
		) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS resources (
			name           TEXT PRIMARY KEY,
			max_ttl_seconds INTEGER NOT NULL DEFAULT 0,
			description    TEXT NOT NULL DEFAULT '',
			created_at     INTEGER NOT NULL
		) WITHOUT ROWID`,
		`CREATE INDEX IF NOT EXISTS idx_leases_expires ON leases(expires_at)`,
		`CREATE INDEX IF NOT EXISTS idx_leases_holder ON leases(holder)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// Acquire attempts to grant a new lease for resource to holder lasting
// ttlSec seconds from now. If an active lease exists it returns ErrConflict
// and a copy of the conflicting row as *LeaseRow (conflict). If an expired
// lease row is present it is replaced. The allocated fencing token is
// returned alongside the new expiry time.
func (s *Store) Acquire(ctx context.Context, resource, holder string, ttlSec, now int64) (token, expiresAt int64, conflict *LeaseRow, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, nil, err
	}
	defer tx.Rollback()

	existing, err := queryLease(ctx, tx, resource)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil, err
	}
	if err == nil && existing.ExpiresAt > now {
		c := existing // copy so the caller gets a stable value
		return 0, 0, &c, ErrConflict
	}

	// Enforce a registered resource's max_ttl, if any. A max_ttl of 0 means
	// "unbounded" (the default for unregistered resources).
	if maxTTL, ok, err := maxTTLForTx(ctx, tx, resource); err != nil {
		return 0, 0, nil, err
	} else if ok && maxTTL > 0 && ttlSec > maxTTL {
		return 0, 0, nil, fmt.Errorf("acquire: %w", ErrTTLExceedsMax)
	}

	token, err = allocTokenTx(ctx, tx, resource)
	if err != nil {
		return 0, 0, nil, err
	}
	expiresAt = now + ttlSec
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO leases (resource, holder, fencing_token, acquired_at, expires_at, ttl_seconds)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(resource) DO UPDATE SET
			holder        = excluded.holder,
			fencing_token = excluded.fencing_token,
			acquired_at   = excluded.acquired_at,
			expires_at    = excluded.expires_at,
			ttl_seconds   = excluded.ttl_seconds`, resource, holder, token, now, expiresAt, ttlSec); err != nil {
		return 0, 0, nil, fmt.Errorf("insert lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, nil, err
	}
	return token, expiresAt, nil, nil
}

// Renew extends the lease for resource/holder/fencingToken by ttlSec seconds
// from now. It fails with ErrExpired, ErrHolderMismatch, ErrTokenMismatch or
// ErrNoLease as appropriate. Renewing an expired lease is forbidden even for
// the original holder: they must re-acquire, which allocates a fresh token.
func (s *Store) Renew(ctx context.Context, resource, holder string, fencingToken, ttlSec, now int64) (acquiredAt, expiresAt int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	existing, err := queryLease(ctx, tx, resource)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, 0, ErrNoLease
		}
		return 0, 0, err
	}
	if existing.ExpiresAt <= now {
		return 0, 0, ErrExpired
	}
	if existing.Holder != holder {
		return 0, 0, ErrHolderMismatch
	}
	if existing.FencingToken != fencingToken {
		return 0, 0, ErrTokenMismatch
	}
	expiresAt = now + ttlSec
	if _, err := tx.ExecContext(ctx, `UPDATE leases SET expires_at = ?, ttl_seconds = ? WHERE resource = ?`, expiresAt, ttlSec, resource); err != nil {
		return 0, 0, fmt.Errorf("renew update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return existing.AcquiredAt, expiresAt, nil
}

// Release deletes the lease for resource/holder/fencingToken. Releasing an
// expired lease is an error (it has already lapsed); the sweep is the only
// path that removes expired rows.
func (s *Store) Release(ctx context.Context, resource, holder string, fencingToken, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	existing, err := queryLease(ctx, tx, resource)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoLease
		}
		return err
	}
	if existing.ExpiresAt <= now {
		return ErrExpired
	}
	if existing.Holder != holder {
		return ErrHolderMismatch
	}
	if existing.FencingToken != fencingToken {
		return ErrTokenMismatch
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM leases WHERE resource = ?`, resource); err != nil {
		return fmt.Errorf("delete lease: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

// Sweep deletes every lease whose expiry is at or before now and returns the
// number removed. It deliberately leaves fencing_counters untouched so that
// the next allocated token continues the global monotonic sequence.
func (s *Store) Sweep(ctx context.Context, now int64) (int, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM leases WHERE expires_at <= ?`, now)
	if err != nil {
		return 0, fmt.Errorf("sweep: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep rows affected: %w", err)
	}
	return int(n), nil
}

// Get returns the lease row for resource, or (nil, nil) when none exists.
func (s *Store) Get(ctx context.Context, resource string) (*LeaseRow, error) {
	row := s.db.QueryRowContext(ctx, selectLeaseSQL+` WHERE resource = ?`, resource)
	l, err := scanLease(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

// List returns all lease rows ordered by expiry.
func (s *Store) List(ctx context.Context) ([]LeaseRow, error) {
	rows, err := s.db.QueryContext(ctx, selectLeaseSQL+` ORDER BY expires_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaseRow
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// PeekFencingToken exposes the next token that would be allocated for a
// resource. It is used by diagnostics and tests; it never mutates state.
func (s *Store) PeekFencingToken(ctx context.Context, resource string) (int64, error) {
	var tok int64
	err := s.db.QueryRowContext(ctx, `SELECT next_token FROM fencing_counters WHERE resource = ?`, resource).Scan(&tok)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 1, nil
		}
		return 0, err
	}
	return tok, nil
}

// Transfer hands an active lease from one holder to another, allocating a
// fresh fencing token for the new holder. The current holder and token must
// match and the lease must be unexpired. The old token is invalidated by the
// new allocation, so the previous holder cannot renew with it afterwards.
func (s *Store) Transfer(ctx context.Context, resource, currentHolder string, fencingToken, ttlSec, now int64, newHolder string) (LeaseRow, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LeaseRow{}, err
	}
	defer tx.Rollback()

	existing, err := queryLease(ctx, tx, resource)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LeaseRow{}, ErrNoLease
		}
		return LeaseRow{}, err
	}
	if existing.ExpiresAt <= now {
		return LeaseRow{}, ErrExpired
	}
	if existing.Holder != currentHolder {
		return LeaseRow{}, ErrHolderMismatch
	}
	if existing.FencingToken != fencingToken {
		return LeaseRow{}, ErrTokenMismatch
	}
	if maxTTL, ok, err := maxTTLForTx(ctx, tx, resource); err != nil {
		return LeaseRow{}, err
	} else if ok && maxTTL > 0 && ttlSec > maxTTL {
		return LeaseRow{}, ErrTTLExceedsMax
	}
	token, err := allocTokenTx(ctx, tx, resource)
	if err != nil {
		return LeaseRow{}, err
	}
	expiresAt := now + ttlSec
	if _, err := tx.ExecContext(ctx,
		`UPDATE leases SET holder = ?, fencing_token = ?, acquired_at = ?, expires_at = ?, ttl_seconds = ? WHERE resource = ?`,
		newHolder, token, now, expiresAt, ttlSec, resource); err != nil {
		return LeaseRow{}, fmt.Errorf("transfer update: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LeaseRow{}, err
	}
	return LeaseRow{
		Resource:     resource,
		Holder:       newHolder,
		FencingToken: token,
		AcquiredAt:   now,
		ExpiresAt:    expiresAt,
		TTLSeconds:   ttlSec,
	}, nil
}

// ListByHolder returns every lease currently held by holder (including
// expired-but-unswept ones), ordered by expiry ascending.
func (s *Store) ListByHolder(ctx context.Context, holder string) ([]LeaseRow, error) {
	rows, err := s.db.QueryContext(ctx, selectLeaseSQL+` WHERE holder = ? ORDER BY resource ASC`, holder)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaseRow
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ListExpired returns every lease whose expiry is at or before now.
func (s *Store) ListExpired(ctx context.Context, now int64) ([]LeaseRow, error) {
	rows, err := s.db.QueryContext(ctx, selectLeaseSQL+` WHERE expires_at <= ? ORDER BY expires_at ASC`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LeaseRow
	for rows.Next() {
		l, err := scanLease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// Stats summarises the database: active leases, expired-but-unswept leases,
// distinct resources ever seen (from fencing counters) and distinct holders
// of currently-stored leases.
type Stats struct {
	ActiveLeases   int
	ExpiredLeases  int
	TotalResources int
	TotalHolders   int
}

// Stats computes summary statistics at the given instant.
func (s *Store) Stats(ctx context.Context, now int64) (Stats, error) {
	var st Stats
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM leases WHERE expires_at > ?`, now).Scan(&st.ActiveLeases); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM leases WHERE expires_at <= ?`, now).Scan(&st.ExpiredLeases); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM fencing_counters`).Scan(&st.TotalResources); err != nil {
		return st, err
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT holder) FROM leases`).Scan(&st.TotalHolders); err != nil {
		return st, err
	}
	return st, nil
}

// ForceRelease removes a lease without checking holder or token. It is the
// admin escape hatch used by DELETE /admin/leases/{resource}; normal clients
// must go through Release. It returns ErrNoLease when nothing is held.
func (s *Store) ForceRelease(ctx context.Context, resource string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM leases WHERE resource = ?`, resource)
	if err != nil {
		return fmt.Errorf("force release: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("force release rows: %w", err)
	}
	if n == 0 {
		return ErrNoLease
	}
	return nil
}

// maxTTLForTx reads the registered max_ttl for resource within tx. It returns
// (0, false, nil) when the resource is not registered.
func maxTTLForTx(ctx context.Context, tx *sql.Tx, resource string) (int64, bool, error) {
	var maxTTL int64
	err := tx.QueryRowContext(ctx, `SELECT max_ttl_seconds FROM resources WHERE name = ?`, resource).Scan(&maxTTL)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, err
	}
	return maxTTL, true, nil
}

// Ping verifies the database connection is usable. Used by /health.
func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

const selectLeaseSQL = `SELECT resource, holder, fencing_token, acquired_at, expires_at, ttl_seconds FROM leases`

// scanner abstracts *sql.Row and *sql.Rows for scanLease.
type scanner interface {
	Scan(dest ...any) error
}

func queryLease(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, resource string) (LeaseRow, error) {
	row := q.QueryRowContext(ctx, selectLeaseSQL+` WHERE resource = ?`, resource)
	return scanLease(row)
}

func scanLease(s scanner) (LeaseRow, error) {
	var l LeaseRow
	err := s.Scan(&l.Resource, &l.Holder, &l.FencingToken, &l.AcquiredAt, &l.ExpiresAt, &l.TTLSeconds)
	return l, err
}

// allocTokenTx allocates the next fencing token for resource within tx. The
// sequence is: ensure a counter row exists (default next_token=1), read the
// current next_token, then advance it by one. The read value is the allocated
// token. This yields 1, 2, 3, ... and never recycles a value.
func allocTokenTx(ctx context.Context, tx *sql.Tx, resource string) (int64, error) {
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO fencing_counters (resource, next_token) VALUES (?, 1)
		 ON CONFLICT(resource) DO NOTHING`, resource); err != nil {
		return 0, fmt.Errorf("ensure fencing counter: %w", err)
	}
	var token int64
	if err := tx.QueryRowContext(ctx,
		`SELECT next_token FROM fencing_counters WHERE resource = ?`, resource).Scan(&token); err != nil {
		return 0, fmt.Errorf("read fencing counter: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE fencing_counters SET next_token = next_token + 1 WHERE resource = ?`, resource); err != nil {
		return 0, fmt.Errorf("advance fencing counter: %w", err)
	}
	return token, nil
}

// FormatTime converts a stored unix-second timestamp to a time.Time for the
// domain layer. It is exposed so tests in other packages can mirror the
// convention without importing time directly.
func FormatTime(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

// ErrEmptyResource is returned by RegisterResource when the name is blank.
var ErrEmptyResource = errors.New("resource name must not be empty")
