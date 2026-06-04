package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
	"github.com/Kelvris/ft/transport"
	"github.com/Kelvris/ft/util"
	"github.com/Kelvris/ft/version"

	"github.com/spf13/cobra"
)

var (
	pushPassword  bool
	pushDryRun    bool
	pushJobs      int
	pushNoDelete  bool
	pushQuiet     bool
	pushInclude   []string
	pushExclude   []string
	pushNoVersion bool
)

type passwordSource int

const (
	passwordNone   passwordSource = iota
	passwordFlag
	passwordEnv
	passwordConfig
	passwordVault
)

var pwdSource passwordSource

var pushCmd = &cobra.Command{
	Use:   "push [remote] [files...]",
	Short: "Upload changed files to remote server",
	Long: `Uploads added, modified, and deleted files to the remote server,
then syncs the file index. Default remote is "origin".

You can specify specific files/directories to push selectively:
  ft push origin admin/categories.php
  ft push --include '*.php' --exclude 'admin/*'`,
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
			if err := os.MkdirAll(util.FtDir, 0755); err != nil {
				return fmt.Errorf("creating .ft directory: %w", err)
			}
			if err := index.New().Save(); err != nil {
				return fmt.Errorf("saving index: %w", err)
			}
			cfg := &config.Config{Remotes: make(map[string]*config.Remote)}
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
			if !pushQuiet {
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

		pwdSource = passwordNone
		if err := resolvePassword(remote, remoteName); err != nil {
			return err
		}

		ignorePatterns, err := index.LoadIgnorePatterns()
		if err != nil {
			return err
		}
		ignorePatterns = append(ignorePatterns, pushExclude...)

		changes, err := index.DetectChanges(".", ignorePatterns)
		if err != nil {
			return fmt.Errorf("detecting changes: %w", err)
		}

		changes = filterChanges(changes, fileArgs, pushInclude)

		var toUpload []index.Change
		var toDelete []index.Change
		for _, c := range changes {
			if c.Type == index.Deleted && isProtectedPath(c.Path) {
				continue
			}
			if pushNoDelete && c.Type == index.Deleted {
				continue
			}
			switch c.Type {
			case index.Added, index.Modified:
				toUpload = append(toUpload, c)
			case index.Deleted:
				toDelete = append(toDelete, c)
			}
		}

		total := len(toUpload) + len(toDelete)

		if len(toUpload) == 0 && len(toDelete) == 0 {
			if !pushQuiet {
				fmt.Println("nothing to push (working tree clean)")
			}
			return nil
		}

		versionName := ""
		if !pushNoVersion && (len(toUpload) > 0 || len(toDelete) > 0) {
			versionName = time.Now().UTC().Format("20060102-150405")
		}

		if pushDryRun {
			if !pushQuiet {
				fmt.Printf("dry run: would push %d file(s)\n", total)
			}
			for _, c := range toUpload {
				fmt.Printf("  upload   %s\n", c.Path)
			}
			for _, c := range toDelete {
				fmt.Printf("  delete   %s\n", c.Path)
			}
			return nil
		}

		if pushJobs < 1 {
			pushJobs = 4
		}

		if versionName != "" && len(toDelete) > 0 {
			tr, err := transport.NewTransport(remote)
			if err == nil {
				if err := tr.Connect(); err == nil {
					deletePaths := make([]string, len(toDelete))
					for i, c := range toDelete {
						deletePaths[i] = c.Path
					}
					version.BackupDeletedFiles(versionName, deletePaths, tr)
				}
				tr.Close()
			}
		}

		var completed int64
		var wg sync.WaitGroup
		uploads := make(chan string, len(toUpload))
		errCh := make(chan error, len(toUpload))

		for i := 0; i < pushJobs; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				tr, err := transport.NewTransport(remote)
				if err != nil {
					errCh <- err
					return
				}
				defer tr.Close()
				if err := tr.Connect(); err != nil {
					errCh <- fmt.Errorf("worker %d connect: %w", id, err)
					return
				}

				for path := range uploads {
					count := atomic.AddInt64(&completed, 1)
					if !pushQuiet {
						fmt.Printf("[%d/%d] uploading %s\n", count, total, path)
					}
					if err := retryUpload(tr, path, path, 3); err != nil {
						errCh <- fmt.Errorf("uploading %s: %w", path, err)
						return
					}
					if versionName != "" {
						if err := saveVersionFile(versionName, path); err != nil {
							fmt.Fprintf(os.Stderr, "warning: saving version file %s: %v\n", path, err)
						}
					}
				}
			}(i)
		}

		for _, c := range toUpload {
			uploads <- c.Path
		}
		close(uploads)
		wg.Wait()
		close(errCh)

		for err := range errCh {
			return err
		}

		if len(toDelete) > 0 {
			tr, err := transport.NewTransport(remote)
			if err != nil {
				return err
			}
			if err := tr.Connect(); err != nil {
				tr.Close()
				return fmt.Errorf("connecting for deletes: %w", err)
			}
			defer tr.Close()
			for _, c := range toDelete {
				count := atomic.AddInt64(&completed, 1)
				if !pushQuiet {
					fmt.Printf("[%d/%d] deleting  %s\n", count, total, c.Path)
				}
				if err := tr.Delete(c.Path); err != nil {
					fmt.Fprintf(os.Stderr, "warning: deleting %s: %v\n", c.Path, err)
				}
			}
		}

		newIdx, err := index.BuildIndex(".", ignorePatterns)
		if err != nil {
			return fmt.Errorf("building index: %w", err)
		}
		if err := newIdx.Save(); err != nil {
			return fmt.Errorf("saving index: %w", err)
		}

		tr, err := transport.NewTransport(remote)
		if err != nil {
			return err
		}
		if err := tr.Connect(); err != nil {
			tr.Close()
			return fmt.Errorf("connecting for index sync: %w", err)
		}
		defer tr.Close()

		if err := transport.SyncIndexToRemote(tr, newIdx); err != nil {
			fmt.Fprintf(os.Stderr, "warning: syncing remote index: %v\n", err)
		}

		if versionName != "" {
			if err := version.Save(versionName); err != nil {
				fmt.Fprintf(os.Stderr, "warning: saving version %s: %v\n", versionName, err)
			}
			syncVersionToRemote(versionName, tr)
		}

		if pwdSource == passwordVault {
			util.RotateSecret(remoteName)
		}

		if !pushQuiet {
			fmt.Printf("\npushed %d files to %q\n", total, remoteName)
		}
		return nil
	},
}

func saveVersionFile(versionName, path string) error {
	src := filepath.FromSlash(path)
	dst := version.VersionPath(versionName, "files", path)
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func syncVersionToRemote(versionName string, t transport.Transport) {
	localDir := version.VersionPath(versionName)
	filepath.Walk(localDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(localDir, p)
		if err != nil {
			return nil
		}
		remoteRel := ".ft/versions/" + versionName + "/" + filepath.ToSlash(rel)
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		return t.WriteFile(remoteRel, data)
	})
}

func filterChanges(changes []index.Change, fileArgs, includePatterns []string) []index.Change {
	if len(fileArgs) == 0 && len(includePatterns) == 0 {
		return changes
	}

	var filtered []index.Change
	for _, c := range changes {
		if len(fileArgs) > 0 {
			match := false
			for _, arg := range fileArgs {
				arg = filepath.ToSlash(arg)
				if c.Path == arg || strings.HasPrefix(c.Path, arg+"/") {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		if len(includePatterns) > 0 {
			match := false
			for _, pat := range includePatterns {
				if matched, _ := filepath.Match(pat, c.Path); matched {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		filtered = append(filtered, c)
	}
	return filtered
}

func retryUpload(t transport.Transport, local, remote string, attempts int) error {
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := t.Upload(local, remote, nil); err != nil {
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

func resolvePassword(remote *config.Remote, name string) error {
	if pushPassword {
		pwd, err := promptPassword()
		if err != nil {
			return err
		}
		remote.Password = pwd
		pwdSource = passwordFlag
		return nil
	}

	if pwd := util.GetEnvPassword(); pwd != "" {
		remote.Password = pwd
		pwdSource = passwordEnv
		return nil
	}

	if remote.Password != "" {
		pwdSource = passwordConfig
		return nil
	}

	pwd, err := util.LoadPassword(name)
	if err != nil {
		return fmt.Errorf("loading saved password: %w", err)
	}
	if pwd != "" {
		remote.Password = pwd
		pwdSource = passwordVault
	}

	return nil
}

func isProtectedPath(path string) bool {
	return strings.HasPrefix(path, ".ft/") || path == ".ft" ||
		strings.HasPrefix(path, ".git/") || path == ".git"
}

func init() {
	pushCmd.Flags().BoolVarP(&pushPassword, "password", "p", false, "Prompt for password")
	pushCmd.Flags().BoolVarP(&pushDryRun, "dry-run", "n", false, "Show what would change without pushing")
	pushCmd.Flags().IntVarP(&pushJobs, "jobs", "j", 4, "Number of concurrent uploads")
	pushCmd.Flags().BoolVar(&pushNoDelete, "no-delete", false, "Do not delete remote files")
	pushCmd.Flags().BoolVarP(&pushQuiet, "quiet", "q", false, "Suppress progress output")
	pushCmd.Flags().StringSliceVar(&pushInclude, "include", nil, "Only include files matching pattern (can repeat)")
	pushCmd.Flags().StringSliceVar(&pushExclude, "exclude", nil, "Exclude files matching pattern (can repeat)")
	pushCmd.Flags().BoolVar(&pushNoVersion, "no-version", false, "Do not auto-create a version snapshot")
	rootCmd.AddCommand(pushCmd)
}
