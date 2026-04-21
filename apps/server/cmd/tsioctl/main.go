// Command tsioctl is the administrative CLI for Test System IO.
package main

import (
	"fmt"
	"os"

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

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
