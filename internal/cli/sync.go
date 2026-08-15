package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yousefakbar/vitald/internal/config"
	"github.com/yousefakbar/vitald/internal/ingest"
	"github.com/yousefakbar/vitald/internal/storage/postgres"
)

func newSyncCommand(version string) *cobra.Command {
	var initialDays int
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Incrementally synchronize all configured health metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			if initialDays < 1 {
				return fmt.Errorf("--initial-days must be at least 1")
			}
			runtime, err := buildRuntime(cmd.Context())
			if err != nil {
				return err
			}
			defer runtime.store.Close()
			return executeSync(cmd, runtime, initialDays, version)
		},
	}
	cmd.Flags().IntVar(&initialDays, "initial-days", 30, "history to fetch when no checkpoint exists")
	return cmd
}

func executeSync(cmd *cobra.Command, runtime *runtime, initialDays int, version string) (returnErr error) {
	ctx := cmd.Context()
	runID, err := runtime.store.StartSyncRun(ctx, postgres.StartSyncRunInput{
		InitialDays: initialDays, Timezone: runtime.cfg.Timezone, VitaldVersion: version,
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Synchronization run %d started\n", runID)

	finalized := false
	defer func() {
		if finalized {
			return
		}
		status := postgres.RunStatusFailed
		if errors.Is(ctx.Err(), context.Canceled) {
			status = postgres.RunStatusCancelled
		}
		cleanupCtx, cancel := historyContext(ctx)
		defer cancel()
		finalErr := runtime.store.CompleteSyncRun(cleanupCtx, postgres.CompleteSyncRunInput{
			ID: runID, Status: status, ErrorMessage: errorString(returnErr),
		})
		if finalErr != nil {
			returnErr = errors.Join(returnErr, finalErr)
		}
	}()

	now := time.Now().In(runtime.service.Location)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, runtime.service.Location).AddDate(0, 0, 1)
	totalRecords := 0
	succeeded := 0
	failed := 0
	var syncErrors []error

	for _, metric := range ingest.Metrics {
		checkpoint, exists, err := runtime.store.Checkpoint(ctx, metric)
		if err != nil {
			return err
		}
		var checkpointBefore *time.Time
		var from time.Time
		if exists {
			checkpointCopy := checkpoint
			checkpointBefore = &checkpointCopy
			from = checkpoint.In(runtime.service.Location).AddDate(0, 0, -2)
		} else {
			from = to.AddDate(0, 0, -initialDays)
		}

		if err := runtime.store.StartSyncMetric(ctx, postgres.StartSyncMetricInput{
			SyncRunID: runID, Metric: metric, RangeStart: from, RangeEnd: to,
			CheckpointBefore: checkpointBefore,
		}); err != nil {
			return err
		}
		if !from.Before(to) {
			if err := runtime.store.CompleteSyncMetric(ctx, postgres.CompleteSyncMetricInput{
				SyncRunID: runID, Metric: metric, Status: postgres.MetricStatusSkipped,
				CheckpointAfter: checkpointBefore,
			}); err != nil {
				return err
			}
			continue
		}

		summary, fetchErr := runtime.service.Fetch(ctx, metric, from, to, true)
		if fetchErr != nil {
			metricStatus := postgres.MetricStatusFailed
			if errors.Is(fetchErr, context.Canceled) {
				metricStatus = postgres.MetricStatusCancelled
			}
			cleanupCtx, cancel := historyContext(ctx)
			completeErr := runtime.store.CompleteSyncMetric(cleanupCtx, postgres.CompleteSyncMetricInput{
				SyncRunID: runID, Metric: metric, Status: metricStatus,
				PagesArchived: summary.Pages, RecordsProcessed: summary.Records,
				ErrorMessage: fetchErr.Error(),
			})
			cancel()
			if completeErr != nil {
				return errors.Join(fetchErr, completeErr)
			}
			if metricStatus == postgres.MetricStatusCancelled {
				return fetchErr
			}
			failed++
			syncErrors = append(syncErrors, fmt.Errorf("%s: %w", metric, fetchErr))
			fmt.Fprintf(cmd.ErrOrStderr(), "%s: failed: %v\n", metric, fetchErr)
			continue
		}

		checkpointAfter := to
		if err := runtime.store.CompleteSyncMetric(ctx, postgres.CompleteSyncMetricInput{
			SyncRunID: runID, Metric: metric, Status: postgres.MetricStatusSucceeded,
			CheckpointAfter: &checkpointAfter, PagesArchived: summary.Pages,
			RecordsProcessed: summary.Records,
		}); err != nil {
			return err
		}
		succeeded++
		totalRecords += summary.Records
		fmt.Fprintf(cmd.OutOrStdout(), "%s: %d records\n", metric, summary.Records)
	}

	runStatus := syncRunStatus(succeeded, failed)
	joinedErr := errors.Join(syncErrors...)
	cleanupCtx, cancel := historyContext(ctx)
	err = runtime.store.CompleteSyncRun(cleanupCtx, postgres.CompleteSyncRunInput{
		ID: runID, Status: runStatus, ErrorMessage: errorString(joinedErr),
	})
	cancel()
	if err != nil {
		return errors.Join(joinedErr, err)
	}
	finalized = true
	fmt.Fprintf(cmd.OutOrStdout(), "Synchronization run %d complete: %d records processed, %d metric(s) failed\n", runID, totalRecords, failed)
	return joinedErr
}

func syncRunStatus(succeeded, failed int) postgres.RunStatus {
	switch {
	case failed == 0:
		return postgres.RunStatusSucceeded
	case succeeded == 0:
		return postgres.RunStatusFailed
	default:
		return postgres.RunStatusPartial
	}
}

func historyContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show synchronization status and checkpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, store, location, err := openStatusStore(cmd.Context())
			if err != nil {
				return err
			}
			defer store.Close()

			latest, found, err := store.LatestSyncRun(cmd.Context())
			if err != nil {
				return err
			}
			if found {
				printSyncRunSummary(cmd, latest, location, "Last synchronization")
			} else {
				fmt.Fprintln(cmd.OutOrStdout(), "No synchronization runs found.")
			}

			status, err := store.Status(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "\nCheckpoints (%s)\n", cfg.Timezone)
			if len(status) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "  No synchronization checkpoints found.")
				return nil
			}
			for _, metric := range ingest.Metrics {
				if value, ok := status[metric]; ok {
					fmt.Fprintf(cmd.OutOrStdout(), "  %-30s %s\n", metric, value.In(location).Format(time.RFC3339))
				}
			}
			return nil
		},
	}
}

func openStatusStore(ctx context.Context) (config.Config, *postgres.Store, *time.Location, error) {
	cfg, err := config.Load()
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	if cfg.DatabaseURL == "" {
		return config.Config{}, nil, nil, fmt.Errorf("DATABASE_URL is required")
	}
	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return config.Config{}, nil, nil, err
	}
	if err := store.Migrate(ctx); err != nil {
		store.Close()
		return config.Config{}, nil, nil, err
	}
	location, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		store.Close()
		return config.Config{}, nil, nil, err
	}
	return cfg, store, location, nil
}

func printSyncRunSummary(cmd *cobra.Command, run postgres.SyncRunSummary, location *time.Location, heading string) {
	duration := "running"
	if run.CompletedAt != nil {
		duration = run.CompletedAt.Sub(run.StartedAt).Round(time.Millisecond).String()
	}
	fmt.Fprintln(cmd.OutOrStdout(), heading)
	fmt.Fprintf(cmd.OutOrStdout(), "  ID:         %d\n", run.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "  Started:    %s\n", run.StartedAt.In(location).Format(time.RFC3339))
	fmt.Fprintf(cmd.OutOrStdout(), "  Duration:   %s\n", duration)
	fmt.Fprintf(cmd.OutOrStdout(), "  Status:     %s\n", run.Status)
	fmt.Fprintf(cmd.OutOrStdout(), "  Metrics:    %d succeeded, %d failed, %d cancelled, %d skipped\n",
		run.MetricsSucceeded, run.MetricsFailed, run.MetricsCancelled, run.MetricsSkipped)
	fmt.Fprintf(cmd.OutOrStdout(), "  Records:    %d\n", run.RecordsProcessed)
	fmt.Fprintf(cmd.OutOrStdout(), "  Pages:      %d\n", run.PagesArchived)
	if run.ErrorMessage != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Error:      %s\n", strings.ReplaceAll(run.ErrorMessage, "\n", " "))
	}
}
