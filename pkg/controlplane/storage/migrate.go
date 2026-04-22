package storage

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// applyMigrations runs every migrations/*.sql file in lexical order, recording
// applied versions in schema_migrations so re-runs are idempotent.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("storage: acquire for migrations: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS schema_migrations (
            version TEXT PRIMARY KEY,
            applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )
    `); err != nil {
		return fmt.Errorf("storage: create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("storage: list migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		if err := applyOne(ctx, conn.Conn(), name); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, conn *pgx.Conn, name string) error {
	var seen string
	err := conn.QueryRow(ctx, `SELECT version FROM schema_migrations WHERE version = $1`, name).Scan(&seen)
	if err == nil {
		return nil // already applied
	}
	body, err := migrationsFS.ReadFile("migrations/" + name)
	if err != nil {
		return fmt.Errorf("storage: read %s: %w", name, err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("storage: begin %s: %w", name, err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return fmt.Errorf("storage: apply %s: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES ($1)`, name); err != nil {
		return fmt.Errorf("storage: record %s: %w", name, err)
	}
	return tx.Commit(ctx)
}
