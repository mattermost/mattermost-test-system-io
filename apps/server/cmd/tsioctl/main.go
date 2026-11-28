// Command tsioctl is the administrative CLI for Test System IO.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	dbcmd "github.com/mattermost/mattermost-test-system-io/apps/server/cmd/tsioctl/db"
	keyscmd "github.com/mattermost/mattermost-test-system-io/apps/server/cmd/tsioctl/keys"
)

func main() {
	root := &cobra.Command{
		Use:   "tsioctl",
		Short: "Admin CLI for Test System IO",
	}
	root.AddCommand(keyscmd.New())
	root.AddCommand(dbcmd.New())
	root.AddCommand(newOrchestrationCmd())
	root.AddCommand(newStatusShadowCmd())
	root.AddCommand(newBackfillCmd())

	// Cancel on Ctrl-C / SIGTERM so long-running commands (backfill) roll back
	// the in-flight batch and still print their summary.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := root.ExecuteContext(ctx)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
