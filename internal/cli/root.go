package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

func NewRootCommand(build BuildInfo) *cobra.Command {
	var (
		configPath string
		logFormat  string
	)

	cmd := &cobra.Command{
		Use:           "vitald",
		Short:         "Self-hosted health data ingestion",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       build.Version,
	}

	cmd.SetVersionTemplate(fmt.Sprintf(
		"vitald %s\ncommit: %s\nbuilt: %s\n",
		build.Version,
		build.Commit,
		build.Date,
	))

	cmd.PersistentFlags().StringVar(
		&configPath,
		"config",
		"",
		"path to the configuration file",
	)

	cmd.PersistentFlags().StringVar(
		&logFormat,
		"log-format",
		"text",
		"log format: text or json",
	)

	cmd.AddCommand(
		// newAuthCommand(),
		newFetchCommand(),
		// newSyncCommand(),
		// newStatusCommand(),
	)

	return cmd
}
