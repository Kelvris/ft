package cmd

import (
	"fmt"
	"path"
	"strings"

	"ft/config"
	"ft/transport"

	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls [remote[/path]]",
	Short: "List remote directory contents",
	Long: `Lists files and directories on the remote server.

Examples:
  ft ls              list default remote root
  ft ls origin/pub   list a subdirectory`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if len(cfg.Remotes) == 0 {
			return fmt.Errorf("no remotes configured")
		}

		var remoteName, remotePath string

		if len(args) > 0 {
			parts := strings.SplitN(args[0], "/", 2)
			remoteName = parts[0]
			if len(parts) > 1 {
				remotePath = "/" + parts[1]
			}
		} else {
			for name := range cfg.Remotes {
				remoteName = name
				break
			}
			remotePath = ""
		}

		remote, exists := cfg.Remotes[remoteName]
		if !exists {
			return fmt.Errorf("remote %q not found", remoteName)
		}

		t, err := transport.NewTransport(remote)
		if err != nil {
			return err
		}
		if err := t.Connect(); err != nil {
			return fmt.Errorf("connecting: %w", err)
		}
		defer t.Close()

		listPath := remote.RemotePath
		if remotePath != "" {
			listPath = path.Join(remote.RemotePath, remotePath)
		}

		entries, err := t.ListDir(listPath)
		if err != nil {
			return fmt.Errorf("listing %s: %w", listPath, err)
		}

		if len(entries) == 0 {
			fmt.Printf("(empty directory: %s)\n", listPath)
			return nil
		}

		fmt.Printf("contents of %s:%s\n", remoteName, listPath)
		for _, entry := range entries {
			if entry.IsDir {
				fmt.Printf("  %s/\n", entry.Name)
			} else {
				fmt.Printf("  %s\n", entry.Name)
			}
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(lsCmd)
}
