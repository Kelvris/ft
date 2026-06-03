package cmd

import (
	"fmt"
	"os"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
	"github.com/Kelvris/ft/transport"
	"github.com/Kelvris/ft/version"

	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Manage version snapshots",
	Long:  `List, create, and diff version snapshots of your project.`,
}

var versionLsCmd = &cobra.Command{
	Use:   "ls [remote]",
	Short: "List saved versions (local + remote)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remoteName := "origin"
		if len(args) > 0 {
			remoteName = args[0]
		}

		local, _ := version.ListLocal()
		fmt.Printf("Local versions (%d):\n", len(local))
		if len(local) == 0 {
			fmt.Println("  (none)")
		}
		for _, v := range local {
			fmt.Printf("  %s", v.Name)
			if v.CreatedAt != "" {
				fmt.Printf("  (%s)", v.CreatedAt)
			}
			fmt.Println()
		}

		if cfg, err := config.LoadConfig(); err == nil {
			if r, exists := cfg.Remotes[remoteName]; exists {
				if err := resolvePassword(r, remoteName); err != nil {
					fmt.Fprintf(os.Stderr, "warning: resolving password: %v\n", err)
				} else if t, err := transport.NewTransport(r); err == nil {
					if err := t.Connect(); err == nil {
						defer t.Close()
						remote, _ := version.ListRemote(t)
						fmt.Printf("\nRemote versions (%d):\n", len(remote))
						if len(remote) == 0 {
							fmt.Println("  (none)")
						}
						for _, v := range remote {
							fmt.Printf("  %s\n", v)
						}
					}
				}
			}
		}

		return nil
	},
}

var versionSaveCmd = &cobra.Command{
	Use:   "save <name>",
	Short: "Save current state as a version snapshot",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := version.Save(name); err != nil {
			return fmt.Errorf("saving version: %w", err)
		}
		fmt.Printf("saved version %q\n", name)
		return nil
	},
}

var versionDiffCmd = &cobra.Command{
	Use:   "diff <name>",
	Short: "Show files changed in a version vs current",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		verIdx, err := version.LoadVersionIndex(name)
		if err != nil {
			return err
		}

		currentIdx, err := index.Load()
		if err != nil {
			return fmt.Errorf("loading current index: %w", err)
		}

		var added, removed, modified int
		for path, entry := range verIdx.Files {
			if cur, exists := currentIdx.Files[path]; !exists {
				fmt.Printf("  removed:  %s\n", path)
				removed++
			} else if cur.Hash != entry.Hash {
				fmt.Printf("  modified: %s\n", path)
				modified++
			}
		}
		for path := range currentIdx.Files {
			if _, exists := verIdx.Files[path]; !exists {
				fmt.Printf("  added:    %s\n", path)
				added++
			}
		}

		if added+removed+modified == 0 {
			fmt.Println("no difference")
		} else {
			fmt.Printf("\n%d added, %d removed, %d modified\n", added, removed, modified)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.AddCommand(versionLsCmd)
	versionCmd.AddCommand(versionSaveCmd)
	versionCmd.AddCommand(versionDiffCmd)
}
