package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/yousefakbar/vitald/internal/ingest"
)

func newFetchCommand() *cobra.Command {
	var fromValue, toValue string
	cmd := &cobra.Command{
		Use:       "fetch <metric>",
		Short:     "Fetch and persist a Google Health metric",
		Args:      cobra.ExactArgs(1),
		ValidArgs: ingest.Metrics,
		RunE: func(cmd *cobra.Command, args []string) error {
			metric := args[0]
			if !ingest.SupportedMetric(metric) {
				return fmt.Errorf("unsupported metric %q; valid metrics: %s", metric, strings.Join(ingest.Metrics, ", "))
			}
			runtime, err := buildRuntime(cmd.Context())
			if err != nil {
				return err
			}
			defer runtime.store.Close()
			from, err := parseDate(fromValue, runtime.service.Location)
			if err != nil {
				return fmt.Errorf("parse --from: %w", err)
			}
			to, err := parseDate(toValue, runtime.service.Location)
			if err != nil {
				return fmt.Errorf("parse --to: %w", err)
			}
			summary, err := runtime.service.Fetch(cmd.Context(), metric, from, to, false)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Fetched %s: %d records across %d page(s)\n", metric, summary.Records, summary.Pages)
			return nil
		},
	}
	cmd.Flags().StringVar(&fromValue, "from", "", "inclusive start date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&toValue, "to", "", "exclusive end date (YYYY-MM-DD)")
	_ = cmd.MarkFlagRequired("from")
	_ = cmd.MarkFlagRequired("to")
	return cmd
}

func parseDate(value string, location *time.Location) (time.Time, error) {
	parsed, err := time.ParseInLocation(time.DateOnly, value, location)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected YYYY-MM-DD: %w", err)
	}
	return parsed, nil
}
