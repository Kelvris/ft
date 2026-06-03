package cmd

import (
	"fmt"
	"os"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/transport"
	"github.com/Kelvris/ft/version"

	"github.com/spf13/cobra"
)

var revertCmd = &cobra.Command{
	Use:   "revert [name]",
	Short: "Restore files from a version snapshot",
	Long: `Restores your local files to the state saved in a version.
If the version has file backups locally, those are used.
Otherwise files are downloaded from the remote server.
If no name is given, shows an interactive picker.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) > 0 {
			name = args[0]
		} else {
			versions, err := version.ListLocal()
			if err != nil {
				return fmt.Errorf("listing versions: %w", err)
			}
			if len(versions) == 0 {
				return fmt.Errorf("no versions found (use ft push to auto-create or ft version save)")
			}
			name = version.PickVersion(versions)
			if name == "" {
				fmt.Println("cancelled")
				return nil
			}
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		remote, exists := cfg.Remotes["origin"]
		if !exists {
			return fmt.Errorf("no remote configured")
		}

		if err := resolvePassword(remote, "origin"); err != nil {
			fmt.Fprintf(os.Stderr, "warning: resolving password: %v\n", err)
		}

		t, err := transport.NewTransport(remote)
		if err != nil {
			return err
		}
		if err := t.Connect(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not connect to remote: %v\n", err)
			fmt.Println("reverting from local only (some files may be missing)")
			t = nil
		}
		if t != nil {
			defer t.Close()
		}

		return version.Revert(name, t, remote.RemotePath)
	},
}

func init() {
	rootCmd.AddCommand(revertCmd)
}
