package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

type BuildInfo struct{ Version, Commit, Date string }

func NewRootCommand(build BuildInfo) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "vitald",
		Short:         "Collect and own your Google Health data",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       build.Version,
	}
	cmd.SetVersionTemplate(fmt.Sprintf("vitald %s\ncommit: %s\nbuilt: %s\n", build.Version, build.Commit, build.Date))
	cmd.AddCommand(newAuthCommand(), newIdentityCommand(), newFetchCommand(), newSyncCommand(), newStatusCommand())
	return cmd
}
