package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func newRunsCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "runs", Short: "Inspect synchronization run history"}
	cmd.AddCommand(newRunsListCommand(), newRunsShowCommand())
	return cmd
}

func newRunsListCommand() *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List recent synchronization runs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || limit > 1000 {
				return fmt.Errorf("--limit must be between 1 and 1000")
			}
			_, store, location, err := openStatusStore(cmd.Context())
			if err != nil {
				return err
			}
			defer store.Close()
			runs, err := store.ListSyncRuns(cmd.Context(), limit)
			if err != nil {
				return err
			}
			if len(runs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No synchronization runs found.")
				return nil
			}
			fmt.Fprintln(cmd.OutOrStdout(), "ID\tSTARTED\tSTATUS\tMETRICS\tPAGES\tRECORDS\tDURATION")
			for _, run := range runs {
				duration := "running"
				if run.CompletedAt != nil {
					duration = run.CompletedAt.Sub(run.StartedAt).Round(time.Millisecond).String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%d\t%s\t%s\t%d/%d\t%d\t%d\t%s\n",
					run.ID, run.StartedAt.In(location).Format(time.RFC3339), run.Status,
					run.MetricsSucceeded, run.MetricsTotal, run.PagesArchived,
					run.RecordsProcessed, duration)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 20, "maximum runs to return")
	return cmd
}

func newRunsShowCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "show <run-id>",
		Short: "Show one synchronization run and its metrics",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || id < 1 {
				return fmt.Errorf("run ID must be a positive integer")
			}
			_, store, location, err := openStatusStore(cmd.Context())
			if err != nil {
				return err
			}
			defer store.Close()
			run, metrics, found, err := store.SyncRun(cmd.Context(), id)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("synchronization run %d not found", id)
			}
			printSyncRunSummary(cmd, run, location, fmt.Sprintf("Synchronization run %d", run.ID))
			fmt.Fprintln(cmd.OutOrStdout(), "\nMetrics")
			for _, metric := range metrics {
				duration := "running"
				if metric.CompletedAt != nil {
					duration = metric.CompletedAt.Sub(metric.StartedAt).Round(time.Millisecond).String()
				}
				fmt.Fprintf(cmd.OutOrStdout(), "  %-30s %-10s pages=%d records=%d duration=%s\n",
					metric.Metric, metric.Status, metric.PagesArchived, metric.RecordsProcessed, duration)
				if metric.RangeStart != nil && metric.RangeEnd != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "    range: %s to %s\n",
						metric.RangeStart.In(location).Format(time.RFC3339),
						metric.RangeEnd.In(location).Format(time.RFC3339))
				}
				if metric.ErrorMessage != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "    error: %s\n", strings.ReplaceAll(metric.ErrorMessage, "\n", " "))
				}
			}
			return nil
		},
	}
}
