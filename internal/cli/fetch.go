package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newFetchCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fetch",
		Short: "Fetch health data from Google Health",
	}

	cmd.AddCommand(newFetchStepsCommand())

	return cmd
}

func newFetchStepsCommand() *cobra.Command {
	var (
		from string
		to   string
	)

	cmd := &cobra.Command{
		Use:   "steps",
		Short: "Fetch steps data for a time range",
		RunE: func(cmd *cobra.Command, args []string) error {
			if from == "" || to == "" {
				return errors.New("--from and --to are required")
			}

			start, err := time.Parse(time.DateOnly, from)
			if err != nil {
				return fmt.Errorf("parse --from: %w", err)
			}

			end, err := time.Parse(time.DateOnly, to)
			if err != nil {
				return fmt.Errorf("parse --to: %w", err)
			}

			if !start.Before(end) {
				return errors.New("--from must be earlier than --to")
			}

			// The ingestion service will be constructed and called
			// This is just initially a placeholder.
			fmt.Fprintf(
				cmd.OutOrStdout(),
				"fetching steps from %s to %s\n",
				start.Format(time.DateOnly),
				end.Format(time.DateOnly),
			)

			return nil
		},
	}

	cmd.Flags().StringVar(&from, "from", "", "start date in YYYY-MM-DD format")
	cmd.Flags().StringVar(&to, "to", "", "exclusive end date in YYYY-MM-DD format")

	return cmd
}
