package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
	"github.com/Kelvris/ft/transport"
	"github.com/Kelvris/ft/util"
	"github.com/Kelvris/ft/version"

	"github.com/spf13/cobra"
)

var (
	pullPassword bool
	pullDryRun   bool
	pullBackup   bool
	pullQuiet    bool
	pullInclude  []string
	pullExclude  []string
)

var pullPwdSource passwordSource

var pullCmd = &cobra.Command{
	Use:   "pull [remote] [files...]",
	Short: "Download changed files from remote server",
	Long: `Downloads files that differ from the remote index,
then syncs the local index. Default remote is "origin".

Use --backup to save a version before pulling:
  ft pull --backup

Specify files to pull selectively:
  ft pull origin admin/categories.php`,
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		remoteName := "origin"
		var fileArgs []string
		if len(args) > 0 {
			if cfg, _ := config.LoadConfig(); cfg != nil {
				if _, exists := cfg.Remotes[args[0]]; exists {
					remoteName = args[0]
					fileArgs = args[1:]
				} else {
					fileArgs = args
				}
			} else {
				fileArgs = args
			}
		}

		if _, err := os.Stat(util.FtDir); os.IsNotExist(err) {
			os.MkdirAll(util.FtDir, 0755)
			index.New().Save()
			cfg := &config.Config{Remotes: make(map[string]*config.Remote)}
			cfg.Save()
			if !pullQuiet {
				fmt.Println("auto-initialized empty ft project")
			}
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		remote, exists := cfg.Remotes[remoteName]
		if !exists {
			if remoteName == "origin" {
				return fmt.Errorf("no remote configured (use 'ft setup' or 'ft remote add')")
			}
			return fmt.Errorf("remote %q not found", remoteName)
		}

		pullPwdSource = passwordNone
		if err := resolvePullPassword(remote, remoteName); err != nil {
			return err
		}

		if pullBackup {
			backupName := "pre-pull-" + time.Now().UTC().Format("20060102-150405")
			if _, err := index.Load(); err == nil {
				version.Save(backupName)
				if !pullQuiet {
					fmt.Printf("backed up current state as version %q\n", backupName)
				}
			}
		}

		ignorePatterns, err := index.LoadIgnorePatterns()
		if err != nil {
			return err
		}
		ignorePatterns = append(ignorePatterns, pullExclude...)

		t, err := transport.NewTransport(remote)
		if err != nil {
			return err
		}
		if err := t.Connect(); err != nil {
			return fmt.Errorf("connecting: %w", err)
		}
		defer t.Close()

		remoteIdx, err := transport.FetchIndexFromRemote(t)
		if err != nil {
			return fmt.Errorf("fetching remote index: %w", err)
		}

		if !pullQuiet {
			fmt.Printf("remote has %d tracked files\n", len(remoteIdx.Files))
		}

		var toDownload []string
		for relPath, remoteEntry := range remoteIdx.Files {
			if len(fileArgs) > 0 {
				match := false
				for _, arg := range fileArgs {
					arg = filepath.ToSlash(arg)
					if relPath == arg || strings.HasPrefix(relPath, arg+"/") {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}

			localPath := filepath.FromSlash(relPath)

			localExists := true
			localInfo, err := os.Stat(localPath)
			if err != nil {
				if os.IsNotExist(err) {
					localExists = false
				} else {
					fmt.Fprintf(os.Stderr, "warning: checking %s: %v\n", relPath, err)
					continue
				}
			}

			needsDownload := false
			if !localExists {
				needsDownload = true
			} else {
				if localInfo.Size() != remoteEntry.Size || localInfo.ModTime().Unix() != remoteEntry.Mtime {
					localHash, err := index.FileHash(localPath)
					if err != nil {
						fmt.Fprintf(os.Stderr, "warning: hashing %s: %v\n", relPath, err)
						continue
					}
					if localHash != remoteEntry.Hash {
						needsDownload = true
					}
				}
			}

			if needsDownload {
				toDownload = append(toDownload, relPath)
			}
		}

		if len(toDownload) == 0 {
			if !pullQuiet {
				fmt.Println("already up to date")
			}
			return nil
		}

		if pullDryRun {
			if !pullQuiet {
				fmt.Printf("dry run: would pull %d file(s)\n", len(toDownload))
			}
			for _, p := range toDownload {
				fmt.Printf("  download  %s\n", p)
			}
			return nil
		}

		for i, relPath := range toDownload {
			localPath := filepath.FromSlash(relPath)
			if !pullQuiet {
				fmt.Printf("[%d/%d] downloading %s\n", i+1, len(toDownload), relPath)
			}
			if err := retryDownload(t, relPath, localPath, 3); err != nil {
				return fmt.Errorf("downloading %s: %w", relPath, err)
			}
		}

		newIdx, err := index.BuildIndex(".", ignorePatterns)
		if err != nil {
			return fmt.Errorf("building index: %w", err)
		}

		if err := newIdx.Save(); err != nil {
			return fmt.Errorf("saving index: %w", err)
		}

		if pullPwdSource == passwordVault {
			util.RotateSecret(remoteName)
		}

		if !pullQuiet {
			fmt.Printf("\npulled %d files from %q\n", len(toDownload), remoteName)
		}
		return nil
	},
}

func retryDownload(t transport.Transport, remoteRel, localPath string, attempts int) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := t.Download(remoteRel, localPath, nil); err != nil {
			lastErr = err
			if i < attempts-1 {
				time.Sleep(time.Duration(i+1) * time.Second)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func resolvePullPassword(remote *config.Remote, name string) error {
	if pullPassword {
		pwd, err := promptPassword()
		if err != nil {
			return err
		}
		remote.Password = pwd
		pullPwdSource = passwordFlag
		return nil
	}

	if pwd := util.GetEnvPassword(); pwd != "" {
		remote.Password = pwd
		pullPwdSource = passwordEnv
		return nil
	}

	if remote.Password != "" {
		pullPwdSource = passwordConfig
		return nil
	}

	pwd, err := util.LoadPassword(name)
	if err != nil {
		return fmt.Errorf("loading saved password: %w", err)
	}
	if pwd != "" {
		remote.Password = pwd
		pullPwdSource = passwordVault
	}

	return nil
}

func init() {
	pullCmd.Flags().BoolVarP(&pullPassword, "password", "p", false, "Prompt for password")
	pullCmd.Flags().BoolVarP(&pullDryRun, "dry-run", "n", false, "Show what would change without pulling")
	pullCmd.Flags().BoolVarP(&pullQuiet, "quiet", "q", false, "Suppress progress output")
	pullCmd.Flags().BoolVar(&pullBackup, "backup", false, "Save a version backup before pulling")
	pullCmd.Flags().StringSliceVar(&pullInclude, "include", nil, "Only include files matching pattern (can repeat)")
	pullCmd.Flags().StringSliceVar(&pullExclude, "exclude", nil, "Exclude files matching pattern (can repeat)")
	rootCmd.AddCommand(pullCmd)
}
