package cmd

import (
	"fmt"
	"path/filepath"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/transport"

	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <remote-file>",
	Short: "Download a single file from remote",
	Long:  `Downloads one file from the remote server without full pull logic.`,
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remoteFile := args[0]
		remoteName := "origin"

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		remote, exists := cfg.Remotes[remoteName]
		if !exists {
			return fmt.Errorf("remote %q not found", remoteName)
		}

		if err := resolvePullPassword(remote, remoteName); err != nil {
			return err
		}

		t, err := transport.NewTransport(remote)
		if err != nil {
			return err
		}
		if err := t.Connect(); err != nil {
			return fmt.Errorf("connecting: %w", err)
		}
		defer t.Close()

		localPath := filepath.FromSlash(remoteFile)
		fmt.Printf("downloading %s\n", remoteFile)
		if err := t.Download(remoteFile, localPath, nil); err != nil {
			return fmt.Errorf("downloading %s: %w", remoteFile, err)
		}
		fmt.Println("done")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}
