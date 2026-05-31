package cmd

import (
	"fmt"
	"os"

	"github.com/Kelvris/ft/index"

	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show file changes since last sync",
	Long:  `Displays files added, modified, or deleted locally since the last push/pull.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		ignorePatterns, err := index.LoadIgnorePatterns()
		if err != nil {
			return err
		}

		changes, err := index.DetectChanges(".", ignorePatterns)
		if err != nil {
			return fmt.Errorf("detecting changes: %w", err)
		}

		idx, err := index.Load()
		if err != nil {
			return err
		}

		totalFiles := len(idx.Files)
		added := 0
		modified := 0
		deleted := 0

		for _, c := range changes {
			switch c.Type {
			case index.Added:
				added++
			case index.Modified:
				modified++
			case index.Deleted:
				deleted++
			}
		}

		if len(changes) == 0 {
			fmt.Println("working tree clean (no changes since last sync)")
			return nil
		}

		for _, c := range changes {
			marker := ""
			switch c.Type {
			case index.Added:
				marker = "new file:   "
			case index.Modified:
				marker = "modified:   "
			case index.Deleted:
				marker = "deleted:    "
			}
			fmt.Fprintf(os.Stdout, "  %s %s\n", marker, c.Path)
		}

		fmt.Println()
		fmt.Printf("%d files tracked, %d changes: %d added, %d modified, %d deleted\n",
			totalFiles, len(changes), added, modified, deleted)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
