package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusPartial   RunStatus = "partial"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

type MetricStatus string

const (
	MetricStatusRunning   MetricStatus = "running"
	MetricStatusSucceeded MetricStatus = "succeeded"
	MetricStatusFailed    MetricStatus = "failed"
	MetricStatusCancelled MetricStatus = "cancelled"
	MetricStatusSkipped   MetricStatus = "skipped"
)

type StartSyncRunInput struct {
	InitialDays   int
	Timezone      string
	VitaldVersion string
}

type StartSyncMetricInput struct {
	SyncRunID        int64
	Metric           string
	RangeStart       time.Time
	RangeEnd         time.Time
	CheckpointBefore *time.Time
}

type CompleteSyncMetricInput struct {
	SyncRunID        int64
	Metric           string
	Status           MetricStatus
	CheckpointAfter  *time.Time
	PagesArchived    int
	RecordsProcessed int
	ErrorMessage     string
}

type CompleteSyncRunInput struct {
	ID           int64
	Status       RunStatus
	ErrorMessage string
}

type SyncRunSummary struct {
	ID               int64
	StartedAt        time.Time
	CompletedAt      *time.Time
	Status           RunStatus
	InitialDays      int
	Timezone         string
	VitaldVersion    string
	MetricsTotal     int64
	MetricsSucceeded int64
	MetricsFailed    int64
	MetricsCancelled int64
	MetricsSkipped   int64
	PagesArchived    int64
	RecordsProcessed int64
	ErrorMessage     string
}

type SyncMetricSummary struct {
	Metric           string
	StartedAt        time.Time
	CompletedAt      *time.Time
	Status           MetricStatus
	RangeStart       *time.Time
	RangeEnd         *time.Time
	CheckpointBefore *time.Time
	CheckpointAfter  *time.Time
	PagesArchived    int
	RecordsProcessed int
	ErrorMessage     string
}

func (s *Store) StartSyncRun(ctx context.Context, input StartSyncRunInput) (int64, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `
INSERT INTO sync_runs(initial_days, timezone, vitald_version)
VALUES ($1, $2, NULLIF($3, ''))
RETURNING id`, input.InitialDays, input.Timezone, input.VitaldVersion).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("start sync run: %w", err)
	}
	return id, nil
}

func (s *Store) StartSyncMetric(ctx context.Context, input StartSyncMetricInput) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO sync_run_metrics
(sync_run_id, metric, range_start, range_end, checkpoint_before)
VALUES ($1, $2, $3, $4, $5)`, input.SyncRunID, input.Metric, input.RangeStart,
		input.RangeEnd, input.CheckpointBefore)
	if err != nil {
		return fmt.Errorf("start sync metric %s: %w", input.Metric, err)
	}
	return nil
}

func (s *Store) CompleteSyncMetric(ctx context.Context, input CompleteSyncMetricInput) error {
	result, err := s.pool.Exec(ctx, `
UPDATE sync_run_metrics SET
    completed_at = now(),
    status = $3,
    checkpoint_after = $4,
    pages_archived = $5,
    records_processed = $6,
    error_message = NULLIF($7, '')
WHERE sync_run_id = $1 AND metric = $2 AND status = 'running'`,
		input.SyncRunID, input.Metric, input.Status, input.CheckpointAfter,
		input.PagesArchived, input.RecordsProcessed, truncateError(input.ErrorMessage))
	if err != nil {
		return fmt.Errorf("complete sync metric %s: %w", input.Metric, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("complete sync metric %s: running metric not found", input.Metric)
	}
	return nil
}

func (s *Store) CompleteSyncRun(ctx context.Context, input CompleteSyncRunInput) error {
	result, err := s.pool.Exec(ctx, `
UPDATE sync_runs SET
    completed_at = now(),
    status = $2,
    error_message = NULLIF($3, '')
WHERE id = $1 AND status = 'running'`, input.ID, input.Status, truncateError(input.ErrorMessage))
	if err != nil {
		return fmt.Errorf("complete sync run %d: %w", input.ID, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("complete sync run %d: running run not found", input.ID)
	}
	return nil
}

func (s *Store) LatestSyncRun(ctx context.Context) (SyncRunSummary, bool, error) {
	run, err := scanSyncRun(s.pool.QueryRow(ctx, syncRunSummaryQuery+` ORDER BY started_at DESC LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncRunSummary{}, false, nil
	}
	if err != nil {
		return SyncRunSummary{}, false, fmt.Errorf("query latest sync run: %w", err)
	}
	return run, true, nil
}

func (s *Store) ListSyncRuns(ctx context.Context, limit int) ([]SyncRunSummary, error) {
	rows, err := s.pool.Query(ctx, syncRunSummaryQuery+` ORDER BY started_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("list sync runs: %w", err)
	}
	defer rows.Close()
	var runs []SyncRunSummary
	for rows.Next() {
		run, err := scanSyncRun(rows)
		if err != nil {
			return nil, fmt.Errorf("scan sync run: %w", err)
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) SyncRun(ctx context.Context, id int64) (SyncRunSummary, []SyncMetricSummary, bool, error) {
	run, err := scanSyncRun(s.pool.QueryRow(ctx, syncRunSummaryQuery+` WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return SyncRunSummary{}, nil, false, nil
	}
	if err != nil {
		return SyncRunSummary{}, nil, false, fmt.Errorf("query sync run %d: %w", id, err)
	}
	rows, err := s.pool.Query(ctx, `
SELECT metric, started_at, completed_at, status, range_start, range_end,
       checkpoint_before, checkpoint_after, pages_archived, records_processed,
       coalesce(error_message, '')
FROM sync_run_metrics WHERE sync_run_id = $1 ORDER BY started_at, metric`, id)
	if err != nil {
		return SyncRunSummary{}, nil, false, fmt.Errorf("query sync run %d metrics: %w", id, err)
	}
	defer rows.Close()
	var metrics []SyncMetricSummary
	for rows.Next() {
		var metric SyncMetricSummary
		if err := rows.Scan(&metric.Metric, &metric.StartedAt, &metric.CompletedAt, &metric.Status,
			&metric.RangeStart, &metric.RangeEnd, &metric.CheckpointBefore, &metric.CheckpointAfter,
			&metric.PagesArchived, &metric.RecordsProcessed, &metric.ErrorMessage); err != nil {
			return SyncRunSummary{}, nil, false, fmt.Errorf("scan sync metric: %w", err)
		}
		metrics = append(metrics, metric)
	}
	return run, metrics, true, rows.Err()
}

const syncRunSummaryQuery = `
SELECT id, started_at, completed_at, status, initial_days, timezone,
       coalesce(vitald_version, ''), metrics_total, metrics_succeeded,
       metrics_failed, metrics_cancelled, metrics_skipped, pages_archived,
       records_processed, coalesce(error_message, '')
FROM sync_run_history`

type rowScanner interface{ Scan(dest ...any) error }

func scanSyncRun(row rowScanner) (SyncRunSummary, error) {
	var run SyncRunSummary
	err := row.Scan(&run.ID, &run.StartedAt, &run.CompletedAt, &run.Status, &run.InitialDays,
		&run.Timezone, &run.VitaldVersion, &run.MetricsTotal, &run.MetricsSucceeded,
		&run.MetricsFailed, &run.MetricsCancelled, &run.MetricsSkipped, &run.PagesArchived,
		&run.RecordsProcessed, &run.ErrorMessage)
	return run, err
}

func truncateError(message string) string {
	const maxRunes = 8192
	runes := []rune(message)
	if len(runes) <= maxRunes {
		return message
	}
	return string(runes[:maxRunes])
}
