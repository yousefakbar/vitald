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
}
