package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "ft",
	Short: "ft - Git-like FTP/SFTP sync tool",
	Long: `ft syncs local files with remote servers via FTP or SFTP.

Simple, fast, and secure file syncing with:
  - Concurrent uploads with transport-per-worker
  - Password vault (rotating secret directory)
  - Version snapshots for easy rollback
  - Interactive setup wizard
  - Dry-run, quiet mode, selective sync`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
}

func Execute() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("ft v{{.Version}}\n")
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
