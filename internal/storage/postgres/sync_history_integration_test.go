package postgres

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSyncHistoryIntegration(t *testing.T) {
	databaseURL := os.Getenv("VITALD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VITALD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	migrationStatus, err := store.MigrationStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expectedMigrations, err := embeddedMigrationVersions()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrationStatus.Applied) != len(expectedMigrations) || len(migrationStatus.Pending) != 0 || len(migrationStatus.Unknown) != 0 {
		t.Fatalf("unexpected migration status: %+v", migrationStatus)
	}

	runID, err := store.StartSyncRun(ctx, StartSyncRunInput{InitialDays: 7, Timezone: "Asia/Riyadh", VitaldVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	if err := store.StartSyncMetric(ctx, StartSyncMetricInput{SyncRunID: runID, Metric: "steps", RangeStart: from, RangeEnd: to}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSyncMetric(ctx, CompleteSyncMetricInput{SyncRunID: runID, Metric: "steps", Status: MetricStatusSucceeded, CheckpointAfter: &to, PagesArchived: 1, RecordsProcessed: 7}); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteSyncRun(ctx, CompleteSyncRunInput{ID: runID, Status: RunStatusSucceeded}); err != nil {
		t.Fatal(err)
	}

	run, metrics, found, err := store.SyncRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || run.Status != RunStatusSucceeded || run.RecordsProcessed != 7 {
		t.Fatalf("unexpected run: %+v", run)
	}
	if len(metrics) != 1 || metrics[0].PagesArchived != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
	running, err := store.RunningSyncStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if running.Runs != 0 || running.Metrics != 0 {
		t.Fatalf("unexpected running synchronization status: %+v", running)
	}
}

func TestSyncLeaseAndStaleRecoveryIntegration(t *testing.T) {
	databaseURL := os.Getenv("VITALD_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("VITALD_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	other, err := Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	lease, acquired, err := store.TryAcquireSyncLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("expected first synchronization lease to be acquired")
	}

	if secondLease, secondAcquired, err := other.TryAcquireSyncLease(ctx); err != nil {
		t.Fatal(err)
	} else if secondAcquired {
		secondLease.Release(context.Background())
		t.Fatal("second synchronization lease unexpectedly acquired")
	}

	runID, err := store.StartSyncRun(ctx, StartSyncRunInput{InitialDays: 7, Timezone: "Asia/Riyadh", VitaldVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 7)
	if err := store.StartSyncMetric(ctx, StartSyncMetricInput{SyncRunID: runID, Metric: "steps", RangeStart: from, RangeEnd: to}); err != nil {
		t.Fatal(err)
	}

	var backendPID int32
	if err := lease.conn.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&backendPID); err != nil {
		t.Fatal(err)
	}
	var terminated bool
	if err := other.pool.QueryRow(ctx, `SELECT pg_terminate_backend($1)`, backendPID).Scan(&terminated); err != nil {
		t.Fatal(err)
	}
	if !terminated {
		t.Fatal("failed to terminate synchronization lock connection")
	}
	// Release destroys the terminated pooled connection. PostgreSQL has already
	// released its session lock, just as it would after a process crash.
	_ = lease.Release(ctx)

	recoveryLease, recoveryAcquired, err := other.TryAcquireSyncLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !recoveryAcquired {
		t.Fatal("expected synchronization lease after connection termination")
	}
	defer recoveryLease.Release(context.Background())
	recovered, err := recoveryLease.RecoverStaleSyncRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Runs < 1 || recovered.Metrics < 1 {
		t.Fatalf("unexpected recovery counts: %+v", recovered)
	}
	run, metrics, found, err := store.SyncRun(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if !found || run.Status != RunStatusFailed || run.CompletedAt == nil || run.ErrorMessage != staleSyncError {
		t.Fatalf("stale run was not finalized: %+v", run)
	}
	if len(metrics) != 1 || metrics[0].Status != MetricStatusFailed || metrics[0].CompletedAt == nil || metrics[0].ErrorMessage != staleSyncError {
		t.Fatalf("stale metric was not finalized: %+v", metrics)
	}

	if err := recoveryLease.Release(ctx); err != nil {
		t.Fatal(err)
	}
	thirdLease, thirdAcquired, err := store.TryAcquireSyncLease(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !thirdAcquired {
		t.Fatal("expected synchronization lease after release")
	}
	if err := thirdLease.Release(ctx); err != nil {
		t.Fatal(err)
	}
}
