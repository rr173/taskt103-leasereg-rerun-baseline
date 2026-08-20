package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ResourceMeta is the persisted description of a registered resource. A
// resource need not be registered to be leased; registration only attaches a
// MaxTTL cap (enforced on acquire/transfer) and a human description.
type ResourceMeta struct {
	Name        string
	MaxTTL      int64 // seconds; 0 means unbounded
	Description string
	CreatedAt   int64 // unix seconds
}

// RegisterResource inserts (or replaces) the metadata row for name. It is the
// only way to attach a max_ttl; an unregistered resource has no cap.
func (s *Store) RegisterResource(ctx context.Context, name string, maxTTL int64, description string, now int64) error {
	if name == "" {
		return ErrEmptyResource
	}
	if maxTTL < 0 {
		return fmt.Errorf("max_ttl must be non-negative")
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO resources (name, max_ttl_seconds, description, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
		   max_ttl_seconds = excluded.max_ttl_seconds,
		   description     = excluded.description`,
		name, maxTTL, description, now)
	if err != nil {
		return fmt.Errorf("register resource: %w", err)
	}
	return nil
}

// GetResource returns the metadata row for name, or ErrResourceNotFound.
func (s *Store) GetResource(ctx context.Context, name string) (ResourceMeta, error) {
	var m ResourceMeta
	err := s.db.QueryRowContext(ctx,
		`SELECT name, max_ttl_seconds, description, created_at FROM resources WHERE name = ?`, name).
		Scan(&m.Name, &m.MaxTTL, &m.Description, &m.CreatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ResourceMeta{}, ErrResourceNotFound
		}
		return ResourceMeta{}, err
	}
	return m, nil
}

// ListResources returns every registered resource ordered by name.
func (s *Store) ListResources(ctx context.Context) ([]ResourceMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, max_ttl_seconds, description, created_at FROM resources ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResourceMeta
	for rows.Next() {
		var m ResourceMeta
		if err := rows.Scan(&m.Name, &m.MaxTTL, &m.Description, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// UpdateResource changes the max_ttl and/or description of an existing
// resource. It returns ErrResourceNotFound when the resource is not registered.
func (s *Store) UpdateResource(ctx context.Context, name string, maxTTL int64, description string) error {
	if maxTTL < 0 {
		return fmt.Errorf("max_ttl must be non-negative")
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE resources SET max_ttl_seconds = ?, description = ? WHERE name = ?`,
		maxTTL, description, name)
	if err != nil {
		return fmt.Errorf("update resource: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrResourceNotFound
	}
	return nil
}

// DeleteResource removes the metadata row for name. It refuses if an active
// (unexpired) lease still holds the resource, returning ErrResourceInUse.
// Expired-but-unswept leases do not block deletion; call Sweep first to clear
// them. The fencing counter is intentionally NOT removed, preserving the
// monotonic-token guarantee across a re-registration.
func (s *Store) DeleteResource(ctx context.Context, name string, now int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var active int64
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM leases WHERE resource = ?`, name).Scan(&active); err != nil {
		return fmt.Errorf("delete resource: check active: %w", err)
	}
	if active > 0 {
		return ErrResourceInUse
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete resource: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrResourceNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM fencing_counters WHERE resource = ?`, name); err != nil {
		return fmt.Errorf("delete fencing counter: %w", err)
	}
	return tx.Commit()
}
