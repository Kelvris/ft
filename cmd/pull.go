package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/textproto"
	"os"
	"path/filepath"
	"sort"
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
	pullPassword bool
	pullDryRun   bool
	pullBackup   bool
	pullNoBackup bool
	pullQuiet    bool
	pullForce    bool
	pullNoDelete bool
	pullScan     bool
	pullVerify   bool
	pullJobs     int
	pullInclude  []string
	pullExclude  []string
)

var pullPwdSource passwordSource

var pullCmd = &cobra.Command{
	Use:   "pull [remote] [files...]",
	Short: "Download changed files from remote server",
	Long: `Compares local files, the last sync point (.ft/index.json) and the actual
remote filesystem, then fast-forwards the working tree — like "git pull".

Changes that exist only locally are left alone (push carries them). If a file
changed on both sides the pull refuses with a conflict and changes nothing;
run "ft pull --force" to take the remote version (a backup is taken
automatically), or "ft push" to send yours.

Specify files to pull selectively:
  ft pull origin admin/categories.php
  ft pull --include '*.php' --exclude 'admin/*'`,
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

		// Explicit --backup: snapshot the working tree (content included) up
		// front, exactly like the old flag but with something revert can use.
		if pullBackup {
			backupName := "pre-pull-" + time.Now().UTC().Format("20060102-150405")
			if baseIdx, err := index.Load(); err == nil {
				if err := version.SaveWorkingTree(backupName, presentPaths(baseIdx)); err != nil {
					return fmt.Errorf("backup failed, refusing to pull: %w", err)
				}
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

		// The remote index is only a *hint* about server content; the server
		// itself is observed via Stat below.
		remoteIdx, err := transport.FetchIndexFromRemote(t)
		if err != nil {
			return fmt.Errorf("cannot pull: %v\n  Upload files first with:   ft push %s\n  Download single file with: ft restore <path>", err, remoteName)
		}

		if !pullQuiet {
			fmt.Printf("remote has %d tracked files\n", len(remoteIdx.Files))
		}

		baseIdx, err := index.Load()
		if err != nil {
			return fmt.Errorf("loading index: %w", err)
		}

		// --- plan: three sides per path (base / local / remote) -------------
		candidates := pullCandidates(baseIdx, remoteIdx, t, fileArgs, ignorePatterns)
		remoteState := loadRemoteState()
		observed := observeRemote(t, remoteIdx, remoteState, candidates, pullVerify)

		opts := planOptions{Force: pullForce, NoDelete: pullNoDelete}
		var conflicts, toDownload, toDelete, toAdvance []pullPlan
		for _, relPath := range candidates {
			obs, ok := observed[relPath]
			if !ok {
				continue // stat failed → leave the path completely alone
			}
			local, ok := localSide(baseIdx, relPath)
			if !ok {
				continue // unreadable local file → leave alone
			}
			baseEntry := baseIdx.Files[relPath]
			base := pullSide{}
			if baseEntry != nil {
				base = pullSide{Present: true, Hash: baseEntry.Hash}
			}
			plan := planPull(base, local, pullSide{Present: obs.Present, Hash: obs.Hash}, opts)
			plan.Path = relPath
			switch plan.Action {
			case actConflict:
				conflicts = append(conflicts, plan)
			case actDownload:
				toDownload = append(toDownload, plan)
			case actDeleteLocal:
				toDelete = append(toDelete, plan)
			case actAdvanceBase:
				toAdvance = append(toAdvance, plan)
			}
		}

		// Record the observation baseline now; nothing is written until a
		// path below actually reaches a Save (conflict/dry-run return first).
		for relPath, obs := range observed {
			remoteState.record(relPath, obs)
		}

		// --- refuse on conflicts before touching anything -------------------
		if len(conflicts) > 0 {
			renderConflicts(conflicts)
			return exitErr(1, fmt.Errorf("pull refused: %d conflict(s) (no files were changed)", len(conflicts)))
		}

		// --- preview ---------------------------------------------------------
		if pullDryRun {
			if !pullQuiet {
				fmt.Printf("dry run: would pull %d file(s)\n", len(toDownload)+len(toDelete))
				for _, plan := range toDownload {
					fmt.Printf("  download  %s\n", plan.Path)
				}
				for _, plan := range toDelete {
					fmt.Printf("  delete    %s\n", plan.Path)
				}
			}
			return nil
		}

		// --- fast no-op: nothing to transfer, nothing to record -------------
		if len(toDownload) == 0 && len(toDelete) == 0 && len(toAdvance) == 0 {
			// Only the observation baseline can change here; the index is
			// never rebuilt from the working tree (that is what used to
			// absorb untracked files).
			if err := remoteState.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "warning: saving remote state: %v\n", err)
			}
			if !pullQuiet {
				fmt.Println("already up to date")
			}
			if pullPwdSource == passwordVault {
				util.RotateSecret(remoteName)
			}
			return nil
		}

		// --- safety net: snapshot anything about to be overwritten or removed
		overwrite := overwritePaths(toDownload, toDelete)
		if !pullNoBackup && !pullBackup && len(overwrite) > 0 {
			backupName := "pre-pull-" + time.Now().UTC().Format("20060102-150405")
			if err := version.SaveWorkingTree(backupName, overwrite); err != nil {
				return fmt.Errorf("automatic backup failed, refusing to pull: %w", err)
			}
			if !pullQuiet {
				fmt.Printf("backed up %d file(s) as version %q\n", len(overwrite), backupName)
			}
		}

		// --- transfer --------------------------------------------------------
		downloaded, failures := pullDownloads(remote, toDownload)

		var removed []pullPlan
		for _, plan := range toDelete {
			localPath, err := util.LocalPath(".", plan.Path)
			if err != nil {
				failures = append(failures, pullFailure{path: plan.Path, err: err})
				continue
			}
			if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
				failures = append(failures, pullFailure{path: plan.Path, err: err})
				continue
			}
			removed = append(removed, plan)
		}

		// --- surgical index merge (never rebuilt from the working tree) ------
		newIdx := baseIdx
		for relPath, entry := range downloaded {
			newIdx.Files[relPath] = entry
		}
		for _, plan := range removed {
			delete(newIdx.Files, plan.Path)
		}
		for _, plan := range toAdvance {
			if !plan.LocalPresent {
				delete(newIdx.Files, plan.Path)
				continue
			}
			localPath, err := util.LocalPath(".", plan.Path)
			if err != nil {
				failures = append(failures, pullFailure{path: plan.Path, err: err})
				continue
			}
			entry, err := index.EntryFromFile(localPath)
			if err != nil {
				failures = append(failures, pullFailure{path: plan.Path, err: err})
				continue
			}
			newIdx.Files[plan.Path] = entry
		}
		if err := newIdx.Save(); err != nil {
			return fmt.Errorf("saving index: %w", err)
		}

		// --- remember what the server actually looks like --------------------
		for relPath, obs := range observed {
			if !obs.StatOK {
				continue
			}
			if plan, ok := downloaded[relPath]; ok {
				// The download, not any index, is the source of truth.
				obs.Hash = plan.Hash
			}
			remoteState.record(relPath, obs)
		}
		if err := remoteState.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: saving remote state: %v\n", err)
		}

		if pullPwdSource == passwordVault {
			util.RotateSecret(remoteName)
		}

		// --- summary ---------------------------------------------------------
		okCount := len(downloaded) + len(removed)
		if len(failures) > 0 {
			for _, f := range failures {
				fmt.Fprintf(os.Stderr, "  failed  %s: %v\n", f.path, f.err)
			}
			if !pullQuiet {
				fmt.Printf("pulled %d file(s), failed %d, conflicts 0\n", okCount, len(failures))
			}
			return exitErr(2, fmt.Errorf("pull failed for %d file(s)", len(failures)))
		}

		if !pullQuiet {
			if okCount > 0 {
				fmt.Printf("\npulled %d files from %q\n", okCount, remoteName)
			} else {
				fmt.Println("already up to date")
			}
		}
		return nil
	},
}

// presentPaths lists the tracked files that currently exist on disk, for
// content snapshots.
func presentPaths(idx *index.Index) []string {
	var paths []string
	for relPath := range idx.Files {
		localPath, err := util.LocalPath(".", relPath)
		if err != nil {
			continue
		}
		if info, err := os.Stat(localPath); err == nil && !info.IsDir() {
			paths = append(paths, relPath)
		}
	}
	sort.Strings(paths)
	return paths
}

// overwritePaths returns the files a pull is about to destroy: downloads that
// replace a local file plus every local deletion.
func overwritePaths(downloads, deletes []pullPlan) []string {
	var paths []string
	for _, plan := range downloads {
		if plan.LocalPresent {
			paths = append(paths, plan.Path)
		}
	}
	for _, plan := range deletes {
		paths = append(paths, plan.Path)
	}
	sort.Strings(paths)
	return paths
}

// pullCandidates collects every path worth planning: base ∪ remote index ∪
// (with --scan) files discovered on the server, minus ignored/filtered paths.
func pullCandidates(baseIdx, remoteIdx *index.Index, t transport.Transport, fileArgs, ignorePatterns []string) []string {
	seen := make(map[string]bool)
	add := func(p string) { seen[p] = true }
	for p := range baseIdx.Files {
		add(p)
	}
	for p := range remoteIdx.Files {
		add(p)
	}
	if pullScan {
		for _, p := range scanRemoteFiles(t) {
			add(p)
		}
	}

	includeHits := make([]int, len(pullInclude))
	var out []string
	for p := range seen {
		if index.IsIgnored(p, ignorePatterns) {
			continue
		}
		if len(fileArgs) > 0 && !matchFileArgs(p, fileArgs) {
			continue
		}
		if len(pullInclude) > 0 {
			matched := false
			for i, pattern := range pullInclude {
				if index.MatchPath(pattern, p) {
					matched = true
					includeHits[i]++
				}
			}
			if !matched {
				continue
			}
		}
		out = append(out, p)
	}
	for i, pattern := range pullInclude {
		if includeHits[i] == 0 {
			fmt.Fprintf(os.Stderr, "warning: --include %q matched 0 files\n", pattern)
		}
	}
	sort.Strings(out)
	return out
}

// matchFileArgs reports whether a path was named on the command line, either
// exactly or as part of a named directory.
func matchFileArgs(relPath string, fileArgs []string) bool {
	for _, arg := range fileArgs {
		arg = filepath.ToSlash(arg)
		if relPath == arg || strings.HasPrefix(relPath, arg+"/") {
			return true
		}
	}
	return false
}

type pullFailure struct {
	path string
	err  error
}

// pullDownloads transfers every planned download with a worker pool (one
// transport per worker — FTP connections are not shareable), verifying content
// hashes and isolating per-file failures so one bad path cannot abort the rest.
func pullDownloads(remote *config.Remote, plans []pullPlan) (map[string]*index.FileEntry, []pullFailure) {
	entries := make(map[string]*index.FileEntry, len(plans))
	var failures []pullFailure
	if len(plans) == 0 {
		return entries, failures
	}

	if pullJobs < 1 {
		pullJobs = 4
	}
	workers := pullJobs
	if workers > len(plans) {
		workers = len(plans)
	}

	type result struct {
		path  string
		entry *index.FileEntry
		err   error
	}
	jobs := make(chan pullPlan, len(plans))
	results := make(chan result, len(plans))
	var completed int64
	var wg sync.WaitGroup

	// F12: transport-level per-file confirmation, suppressed by -q.
	var dlProgress io.Writer
	if !pullQuiet {
		dlProgress = os.Stdout
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr, err := transport.NewTransport(remote)
			if err != nil {
				// Leave the queued jobs to the other workers.
				for plan := range jobs {
					results <- result{path: plan.Path, err: err}
				}
				return
			}
			if err := tr.Connect(); err != nil {
				tr.Close()
				for plan := range jobs {
					results <- result{path: plan.Path, err: fmt.Errorf("connecting: %w", err)}
				}
				return
			}
			defer func() { tr.Close() }()

			for plan := range jobs {
				if !pullQuiet {
					count := atomic.AddInt64(&completed, 1)
					fmt.Printf("[%d/%d] downloading %s\n", count, len(plans), plan.Path)
				}
				localPath, err := util.LocalPath(".", plan.Path)
				if err != nil {
					results <- result{path: plan.Path, err: err}
					continue
				}
				wantHash := ""
				if plan.RemotePresent {
					wantHash = plan.RemoteHash
				}
				if err := pullDownloadOne(&tr, remote, plan.Path, localPath, wantHash, dlProgress); err != nil {
					results <- result{path: plan.Path, err: err}
					continue
				}
				entry, err := index.EntryFromFile(localPath)
				if err != nil {
					results <- result{path: plan.Path, err: err}
					continue
				}
				results <- result{path: plan.Path, entry: entry}
			}
		}()
	}

	for _, plan := range plans {
		jobs <- plan
	}
	close(jobs)
	wg.Wait()
	// Jobs no worker could take (every connection failed) become failures.
	for plan := range jobs {
		results <- result{path: plan.Path, err: errors.New("no connection to remote")}
	}
	close(results)

	for res := range results {
		if res.err != nil {
			failures = append(failures, pullFailure{path: res.path, err: res.err})
			continue
		}
		entries[res.path] = res.entry
	}
	sort.Slice(failures, func(i, j int) bool { return failures[i].path < failures[j].path })
	return entries, failures
}

// pullDownloadOne downloads a single file, retrying only transient errors and
// refreshing the connection between attempts (a dead control connection would
// otherwise fail every retry).
func pullDownloadOne(tr *transport.Transport, remote *config.Remote, relPath, localPath, wantHash string, progress io.Writer) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(pullBackoff(attempt))
			if ntr, err := transport.NewTransport(remote); err == nil {
				if err := ntr.Connect(); err == nil {
					old := *tr
					*tr = ntr
					old.Close()
				} else {
					ntr.Close()
				}
			}
		}
		err := transport.DownloadVerified(*tr, relPath, localPath, wantHash, progress)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isTransientPullError(err) {
			return err
		}
	}
	return lastErr
}

func pullBackoff(attempt int) time.Duration {
	if attempt <= 1 {
		return 250 * time.Millisecond
	}
	return 500 * time.Millisecond
}

// isTransientPullError classifies download errors: permanent failures (missing
// file, permissions) are never retried; everything else gets the small,
// bounded retry budget.
func isTransientPullError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, transport.ErrHashMismatch) {
		return true // corrupt transfer: worth one more attempt
	}
	if errors.Is(err, fs.ErrNotExist) || os.IsNotExist(err) {
		return false
	}
	var textErr *textproto.Error
	if errors.As(err, &textErr) {
		switch textErr.Code {
		case 421, 425, 426, 450, 451, 452:
			return true
		default:
			return false // 550/551/552/530 … permanent
		}
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "no such file"),
		strings.Contains(msg, "not found"):
		return false
	case strings.Contains(msg, "timeout"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "broken pipe"),
		strings.Contains(msg, "unexpected eof"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "server closed"):
		return true
	}
	return true
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
	pullCmd.Flags().BoolVarP(&pullForce, "force", "f", false, "Take the remote version over local edits (backup is automatic)")
	pullCmd.Flags().BoolVar(&pullBackup, "backup", false, "Save a version snapshot (with file contents) before pulling")
	pullCmd.Flags().BoolVar(&pullNoBackup, "no-backup", false, "Skip the automatic pre-overwrite snapshot")
	pullCmd.Flags().BoolVar(&pullNoDelete, "no-delete", false, "Keep local files that were deleted upstream")
	pullCmd.Flags().IntVarP(&pullJobs, "jobs", "j", 4, "Number of concurrent downloads")
	pullCmd.Flags().BoolVar(&pullScan, "scan", false, "Walk the remote tree to find files missing from every index")
	pullCmd.Flags().BoolVar(&pullVerify, "verify", false, "Download each candidate to a temp file and verify its hash")
	pullCmd.Flags().StringSliceVar(&pullInclude, "include", nil, "Only include files matching pattern (can repeat)")
	pullCmd.Flags().StringSliceVar(&pullExclude, "exclude", nil, "Exclude files matching pattern (can repeat)")
	rootCmd.AddCommand(pullCmd)
}
