package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/yousefakbar/vitald/internal/cli"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	root := cli.NewRootCommand(cli.BuildInfo{
		Version: version,
		Commit:  commit,
		Date:    date,
	})

	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "vitald: %v\n", err)
		os.Exit(1)
	}
}
