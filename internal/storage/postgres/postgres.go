package postgres

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct{ pool *pgxpool.Pool }

type Record struct {
	Key              string
	Metric           string
	ProviderRecordID string
	ObservedAt       *time.Time
	EndedAt          *time.Time
	LocalDate        *time.Time
	Value            *float64
	Unit             string
	Attributes       any
	Source           any
	Raw              any
}

type Archive struct {
	RunID, Metric, Path, Checksum string
	Page                          int
	Size                          int64
	From, To                      time.Time
}

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configure PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return fmt.Errorf("initialize schema migrations: %w", err)
	}
	status, err := s.MigrationStatus(ctx)
	if err != nil {
		return err
	}
	if len(status.Unknown) > 0 {
		return fmt.Errorf("database contains migrations unknown to this binary: %s", strings.Join(status.Unknown, ", "))
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read embedded migrations: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var applied bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, entry.Name()).Scan(&applied); err != nil {
			return fmt.Errorf("check migration %s: %w", entry.Name(), err)
		}
		if applied {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("read migration %s: %w", entry.Name(), err)
		}
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name()); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("record migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (s *Store) UpsertRecords(ctx context.Context, records []Record) error {
	if len(records) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	for _, record := range records {
		attributes, err := json.Marshal(record.Attributes)
		if err != nil {
			return fmt.Errorf("encode record attributes: %w", err)
		}
		source, err := json.Marshal(record.Source)
		if err != nil {
			return fmt.Errorf("encode record source: %w", err)
		}
		raw, err := json.Marshal(record.Raw)
		if err != nil {
			return fmt.Errorf("encode raw record: %w", err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO health_records
(record_key, metric, provider_record_id, observed_at, ended_at, local_date, value, unit, attributes, source, raw, imported_at)
VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,NULLIF($8,''),$9,$10,$11,now())
ON CONFLICT (record_key) DO UPDATE SET
 provider_record_id=EXCLUDED.provider_record_id, observed_at=EXCLUDED.observed_at,
 ended_at=EXCLUDED.ended_at, local_date=EXCLUDED.local_date, value=EXCLUDED.value,
 unit=EXCLUDED.unit, attributes=EXCLUDED.attributes, source=EXCLUDED.source,
 raw=EXCLUDED.raw, imported_at=now()`,
			record.Key, record.Metric, record.ProviderRecordID, record.ObservedAt, record.EndedAt,
			record.LocalDate, record.Value, record.Unit, attributes, source, raw)
		if err != nil {
			return fmt.Errorf("upsert %s record: %w", record.Metric, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit records: %w", err)
	}
	return nil
}

func (s *Store) SaveArchive(ctx context.Context, archive Archive) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO raw_archives
(run_id, metric, page_number, range_start, range_end, path, sha256, size_bytes)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
ON CONFLICT (run_id, metric, page_number) DO NOTHING`, archive.RunID, archive.Metric,
		archive.Page, archive.From, archive.To, archive.Path, archive.Checksum, archive.Size)
	if err != nil {
		return fmt.Errorf("save archive metadata: %w", err)
	}
	return nil
}

func (s *Store) Checkpoint(ctx context.Context, metric string) (time.Time, bool, error) {
	var value time.Time
	err := s.pool.QueryRow(ctx, `SELECT synced_through FROM sync_checkpoints WHERE metric=$1`, metric).Scan(&value)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("read %s checkpoint: %w", metric, err)
	}
	return value, true, nil
}

func (s *Store) SetCheckpoint(ctx context.Context, metric string, through time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO sync_checkpoints(metric, synced_through, updated_at)
VALUES ($1,$2,now()) ON CONFLICT(metric) DO UPDATE SET synced_through=EXCLUDED.synced_through, updated_at=now()`, metric, through)
	if err != nil {
		return fmt.Errorf("update %s checkpoint: %w", metric, err)
	}
	return nil
}

func (s *Store) Status(ctx context.Context) (map[string]time.Time, error) {
	rows, err := s.pool.Query(ctx, `SELECT metric, synced_through FROM sync_checkpoints ORDER BY metric`)
	if err != nil {
		return nil, fmt.Errorf("query checkpoints: %w", err)
	}
	defer rows.Close()
	result := make(map[string]time.Time)
	for rows.Next() {
		var metric string
		var value time.Time
		if err := rows.Scan(&metric, &value); err != nil {
			return nil, err
		}
		result[metric] = value
	}
	return result, rows.Err()
}
