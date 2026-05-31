package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
	"github.com/Kelvris/ft/transport"

	"github.com/spf13/cobra"
)

var diffCmd = &cobra.Command{
	Use:   "diff [remote]",
	Short: "Compare local files against remote",
	Long: `Downloads the remote index and shows files that differ.
For modified files, shows a unified diff if 'diff' is available.`,
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

		localIdx, err := index.Load()
		if err != nil {
			return err
		}

		remoteIdx, err := transport.FetchIndexFromRemote(t)
		if err != nil {
			return fmt.Errorf("fetching remote index: %w", err)
		}

		var diffs int

		// Check remote files that differ from local
		for path, remoteEntry := range remoteIdx.Files {
			localEntry, exists := localIdx.Files[path]
			if !exists {
				fmt.Printf("remote-only: %s\n", path)
				diffs++
				continue
			}
			if localEntry.Hash != remoteEntry.Hash {
				fmt.Printf("modified:    %s\n", path)
				showInlineDiff(t, path)
				diffs++
			}
		}

		// Check local-only files
		for path := range localIdx.Files {
			if _, exists := remoteIdx.Files[path]; !exists {
				fmt.Printf("local-only:  %s\n", path)
				diffs++
			}
		}

		if diffs == 0 {
			fmt.Println("local and remote are in sync")
		} else {
			fmt.Printf("\n%d file(s) differ\n", diffs)
		}

		return nil
	},
}

func showInlineDiff(t transport.Transport, path string) {
	tmpDir, err := os.MkdirTemp("", "ft-diff-")
	if err != nil {
		return
	}
	defer os.RemoveAll(tmpDir)

	remotePath := filepath.FromSlash(path)
	localPath := filepath.FromSlash(path)
	tmpFile := filepath.Join(tmpDir, remotePath)

	if err := os.MkdirAll(filepath.Dir(tmpFile), 0755); err != nil {
		return
	}

	if err := t.Download(path, tmpFile, nil); err != nil {
		return
	}

	cmd := exec.Command("diff", "-u", "--label", "local/"+path, "--label", "remote/"+path, localPath, tmpFile)
	out, err := cmd.Output()
	if err == nil || len(out) > 0 {
		fmt.Print(string(out))
	}
}

func init() {
	rootCmd.AddCommand(diffCmd)
}
