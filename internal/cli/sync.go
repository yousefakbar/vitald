package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"github.com/yousefakbar/vitald/internal/config"
	"github.com/yousefakbar/vitald/internal/ingest"
	"github.com/yousefakbar/vitald/internal/storage/postgres"
)

func newSyncCommand() *cobra.Command {
	var initialDays int
	cmd := &cobra.Command{
		Use: "sync", Short: "Incrementally synchronize all configured health metrics",
		RunE: func(cmd *cobra.Command, args []string) error {
			if initialDays < 1 {
				return fmt.Errorf("--initial-days must be at least 1")
			}
			runtime, err := buildRuntime(cmd.Context())
			if err != nil {
				return err
			}
			defer runtime.store.Close()
			now := time.Now().In(runtime.service.Location)
			to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, runtime.service.Location).AddDate(0, 0, 1)
			total := 0
			var syncErrors []error
			for _, metric := range ingest.Metrics {
				from, exists, err := runtime.store.Checkpoint(cmd.Context(), metric)
				if err != nil {
					return err
				}
				if exists {
					from = from.In(runtime.service.Location).AddDate(0, 0, -2)
				} else {
					from = to.AddDate(0, 0, -initialDays)
				}
				if !from.Before(to) {
					continue
				}
				summary, err := runtime.service.Fetch(cmd.Context(), metric, from, to, true)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						return err
					}
					syncErrors = append(syncErrors, fmt.Errorf("%s: %w", metric, err))
					fmt.Fprintf(cmd.ErrOrStderr(), "%s: failed: %v\n", metric, err)
					continue
				}
				total += summary.Records
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %d records\n", metric, summary.Records)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Synchronization complete: %d records processed, %d metric(s) failed\n", total, len(syncErrors))
			return errors.Join(syncErrors...)
		},
	}
	cmd.Flags().IntVar(&initialDays, "initial-days", 30, "history to fetch when no checkpoint exists")
	return cmd
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
		Use: "status", Short: "Show synchronization checkpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.DatabaseURL == "" {
				return fmt.Errorf("DATABASE_URL is required")
			}
			store, err := postgres.Open(cmd.Context(), cfg.DatabaseURL)
			if err != nil {
				return err
			}
			defer store.Close()
			if err := store.Migrate(cmd.Context()); err != nil {
				return err
			}
			location, _ := time.LoadLocation(cfg.Timezone)
			status, err := store.Status(cmd.Context())
			if err != nil {
				return err
			}
			if len(status) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No synchronization checkpoints found.")
				return nil
			}
			for _, metric := range ingest.Metrics {
				if value, ok := status[metric]; ok {
					fmt.Fprintf(cmd.OutOrStdout(), "%-30s %s\n", metric, value.In(location).Format(time.RFC3339))
				}
			}
			return nil
		},
	}
}
