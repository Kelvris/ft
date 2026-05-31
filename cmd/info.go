package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"ft/config"
	"ft/index"
	"ft/transport"
	"ft/util"
	"ft/version"

	"github.com/spf13/cobra"
)

var infoCmd = &cobra.Command{
	Use:   "info [remote]",
	Short: "Show project and remote info",
	Long:  `Displays local index stats, remote info, and version count.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remoteName := "origin"
		if len(args) > 0 {
			remoteName = args[0]
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		fmt.Println("=== Local ===")
		idx, err := index.Load()
		if err != nil {
			fmt.Println("  index: not found (run ft init or ft push)")
		} else {
			fmt.Printf("  tracked files: %d\n", len(idx.Files))
		}

		versions, _ := version.ListLocal()
		fmt.Printf("  local versions: %d\n", len(versions))

		ftSize := dirSize(util.FtDir)
		fmt.Printf("  .ft/ size: %s\n", util.FormatBytes(ftSize))

		if remote, exists := cfg.Remotes[remoteName]; exists {
			fmt.Println("\n=== Remote: " + remoteName + " ===")
			fmt.Printf("  url:      %s://%s@%s:%d%s\n",
				remote.Protocol, remote.Username, remote.Host, remote.Port, remote.RemotePath)

			if err := resolvePassword(remote, remoteName); err == nil {
				t, err := transport.NewTransport(remote)
				if err == nil {
					if err := t.Connect(); err == nil {
						defer t.Close()
						remoteIdx, err := transport.FetchIndexFromRemote(t)
						if err == nil {
							fmt.Printf("  remote files: %d\n", len(remoteIdx.Files))
						}
						remotes, _ := version.ListRemote(t)
						fmt.Printf("  remote versions: %d\n", len(remotes))
						fmt.Println("  connection: OK")
					} else {
						fmt.Println("  connection: FAILED")
					}
				}
			}
		}

		return nil
	},
}

func dirSize(path string) int64 {
	var total int64
	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

func init() {
	rootCmd.AddCommand(infoCmd)
}
