package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type MigrationStatus struct {
	Applied []string
	Pending []string
	Unknown []string
}

type RunningSyncStatus struct {
	Runs    int64
	Metrics int64
}

// MigrationStatus reports migration state without creating tables or applying
// migrations.
func (s *Store) MigrationStatus(ctx context.Context) (MigrationStatus, error) {
	expected, err := embeddedMigrationVersions()
	if err != nil {
		return MigrationStatus{}, err
	}
	status := MigrationStatus{Pending: append([]string(nil), expected...)}

	var migrationsTableExists bool
	if err := s.pool.QueryRow(ctx, `SELECT to_regclass('schema_migrations') IS NOT NULL`).Scan(&migrationsTableExists); err != nil {
		return MigrationStatus{}, fmt.Errorf("check schema migrations table: %w", err)
	}
	if !migrationsTableExists {
		return status, nil
	}

	rows, err := s.pool.Query(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return MigrationStatus{}, fmt.Errorf("query schema migrations: %w", err)
	}
	defer rows.Close()
	var databaseVersions []string
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return MigrationStatus{}, fmt.Errorf("scan schema migration: %w", err)
		}
		databaseVersions = append(databaseVersions, version)
	}
	if err := rows.Err(); err != nil {
		return MigrationStatus{}, fmt.Errorf("query schema migrations: %w", err)
	}

	expectedSet := make(map[string]bool, len(expected))
	for _, version := range expected {
		expectedSet[version] = true
	}
	databaseSet := make(map[string]bool, len(databaseVersions))
	for _, version := range databaseVersions {
		databaseSet[version] = true
		if expectedSet[version] {
			status.Applied = append(status.Applied, version)
		} else {
			status.Unknown = append(status.Unknown, version)
		}
	}
	status.Pending = status.Pending[:0]
	for _, version := range expected {
		if !databaseSet[version] {
			status.Pending = append(status.Pending, version)
		}
	}
	return status, nil
}

func (s *Store) RunningSyncStatus(ctx context.Context) (RunningSyncStatus, error) {
	var status RunningSyncStatus
	err := s.pool.QueryRow(ctx, `
SELECT
    (SELECT count(*) FROM sync_runs WHERE status = 'running'),
    (SELECT count(*) FROM sync_run_metrics WHERE status = 'running')`).Scan(&status.Runs, &status.Metrics)
	if err != nil {
		if errorsIsUndefinedTable(err) {
			return RunningSyncStatus{}, fmt.Errorf("synchronization history schema is not installed")
		}
		return RunningSyncStatus{}, fmt.Errorf("query running synchronization history: %w", err)
	}
	return status, nil
}

func embeddedMigrationVersions() ([]string, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}
	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			versions = append(versions, entry.Name())
		}
	}
	sort.Strings(versions)
	return versions, nil
}

func errorsIsUndefinedTable(err error) bool {
	return pgxErrorCode(err) == "42P01"
}

func pgxErrorCode(err error) string {
	var pgErr interface{ SQLState() string }
	if !errors.As(err, &pgErr) {
		return ""
	}
	return pgErr.SQLState()
}
