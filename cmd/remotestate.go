package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/Kelvris/ft/index"
	"github.com/Kelvris/ft/transport"
	"github.com/Kelvris/ft/util"
)

// remoteFileState is the last *observed* state of a remote file: ft's
// equivalent of git's refs/remotes/origin/*. The remote .ft/index.json is
// only ever consulted as a hint; this file records what the server actually
// looked like the last time we checked.
type remoteFileState struct {
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"` // unix seconds; 0 when the server cannot report it
	Hash  string `json:"hash,omitempty"`
}

type remoteState struct {
	Files map[string]*remoteFileState `json:"files"`
	dirty bool                        // unexported: only written when an observation actually changed
}

func loadRemoteState() *remoteState {
	rs := &remoteState{Files: make(map[string]*remoteFileState)}
	data, err := os.ReadFile(util.FtPath("remote.json"))
	if err != nil {
		return rs
	}
	var parsed remoteState
	if err := json.Unmarshal(data, &parsed); err != nil {
		// A corrupt state file is not fatal: fall back to "never observed".
		return &remoteState{Files: make(map[string]*remoteFileState)}
	}
	if parsed.Files == nil {
		parsed.Files = make(map[string]*remoteFileState)
	}
	return &parsed
}

// Save writes .ft/remote.json, but only when something changed — a no-op
// pull must not touch the filesystem beyond reading it.
func (rs *remoteState) Save() error {
	if !rs.dirty {
		return nil
	}
	if err := util.EnsureDir(util.FtDir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(util.FtPath("remote.json"), data, 0644); err != nil {
		return err
	}
	rs.dirty = false
	return nil
}

// remoteObs is the result of looking at one path on the server.
type remoteObs struct {
	StatOK  bool
	Present bool
	Size    int64
	Mtime   int64  // unix seconds; 0 when unknown
	Hash    string // "" = unknown (only meaningful when Present)
}

// resolveRemoteHash decides which content hash the remote file has.
//
//   - unchanged since we last looked (same size+mtime as .ft/remote.json):
//     trust the hash recorded then.
//   - never observed before: trust the remote index *if* its size matches
//     what the server reports (first-run fast path).
//   - otherwise: unknown (""), which the planner treats as "different" so we
//     never assume an unobserved server file still matches our base.
func resolveRemoteHash(prev *remoteFileState, hint *index.FileEntry, size, mtime int64) string {
	if prev != nil && prev.Hash != "" && remoteStateMatches(prev, size, mtime) {
		// Our last observation still matches the server — unless the remote
		// index, which every push re-syncs, describes the same size with a
		// different hash. Someone pushed in between (same-second, same-size
		// edits are otherwise invisible to one-second mtimes), so the fresh
		// index wins over the stale prev.
		if hint != nil && hint.Size == size && hint.Hash != prev.Hash {
			return hint.Hash
		}
		return prev.Hash
	}
	if prev == nil && hint != nil && hint.Size == size {
		return hint.Hash
	}
	return ""
}

// remoteStateMatches reports whether a recorded observation still describes
// the server. An unknown mtime (0) on either side falls back to size-only.
func remoteStateMatches(prev *remoteFileState, size, mtime int64) bool {
	if prev == nil || prev.Size != size {
		return false
	}
	if prev.Mtime == 0 || mtime == 0 {
		return true
	}
	return prev.Mtime == mtime
}

// observeRemote stats every candidate path on the server and resolves its
// content hash. Paths whose stat fails are omitted so the caller leaves them
// alone; paths reported missing are recorded as absent.
//
// When verify is set, present files are downloaded to a temp file and hashed,
// so even same-size server-side edits are detected (--verify).
func observeRemote(t transport.Transport, remoteIdx *index.Index, prev *remoteState, paths []string, verify bool) map[string]remoteObs {
	out := make(map[string]remoteObs, len(paths))
	for _, relPath := range paths {
		info, err := t.Stat(relPath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				out[relPath] = remoteObs{StatOK: true}
				continue
			}
			fmt.Fprintf(os.Stderr, "warning: checking %s: %v\n", relPath, err)
			continue
		}
		if info.IsDir {
			continue
		}

		obs := remoteObs{StatOK: true, Present: true, Size: info.Size}
		if !info.Mtime.IsZero() {
			obs.Mtime = info.Mtime.Unix()
		}
		obs.Hash = resolveRemoteHash(prev.Files[relPath], remoteIdx.Files[relPath], obs.Size, obs.Mtime)

		if verify {
			if h, ok := verifyRemoteHash(t, relPath); ok {
				obs.Hash = h
			}
		}
		out[relPath] = obs
	}
	return out
}

// verifyRemoteHash downloads a remote file to a scratch dir and returns its
// content hash. The working tree is never touched.
func verifyRemoteHash(t transport.Transport, relPath string) (string, bool) {
	tmpDir, err := os.MkdirTemp("", "ft-verify-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: creating temp dir for %s: %v\n", relPath, err)
		return "", false
	}
	defer os.RemoveAll(tmpDir)

	target, err := util.LocalPath(tmpDir, relPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: unsafe remote path %q: %v\n", relPath, err)
		return "", false
	}
	if err := t.Download(relPath, target, nil); err != nil {
		fmt.Fprintf(os.Stderr, "warning: verifying %s: %v\n", relPath, err)
		return "", false
	}
	hash, err := index.FileHash(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: hashing %s: %v\n", relPath, err)
		return "", false
	}
	return hash, true
}

// scanRemoteFiles walks the remote tree and returns every file path found —
// including files that are in no index at all (created directly on the server).
func scanRemoteFiles(t transport.Transport) []string {
	var found []string
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := t.ListDir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: listing %s: %v\n", dir, err)
			return
		}
		for _, e := range entries {
			if dir == "." && (e.Name == ".ft" || e.Name == ".git") {
				continue
			}
			if e.Name == "." || e.Name == ".." {
				continue
			}
			rel := e.Name
			if dir != "." {
				rel = strings.TrimSuffix(dir, "/") + "/" + e.Name
			}
			if e.IsDir {
				walk(rel)
				continue
			}
			found = append(found, rel)
		}
	}
	walk(".")
	return found
}

// record stores what we learned about a path. Content-unknown observations are
// dropped: a stale record would be worse than no record at all.
func (rs *remoteState) record(relPath string, obs remoteObs) {
	if !obs.StatOK {
		return
	}
	if !obs.Present {
		if _, ok := rs.Files[relPath]; ok {
			delete(rs.Files, relPath)
			rs.dirty = true
		}
		return
	}
	if obs.Hash == "" {
		return
	}
	cur := rs.Files[relPath]
	if cur != nil && cur.Hash == obs.Hash && cur.Size == obs.Size && cur.Mtime == obs.Mtime {
		return
	}
	rs.Files[relPath] = &remoteFileState{Size: obs.Size, Mtime: obs.Mtime, Hash: obs.Hash}
	rs.dirty = true
}

// localSide computes the working-tree side of the comparison for one path.
// It returns ok=false when the path should be skipped (unreadable file);
// callers must then leave that path untouched.
func localSide(baseIdx *index.Index, relPath string) (pullSide, bool) {
	localPath, err := util.LocalPath(".", relPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: skipping unsafe path %q: %v\n", relPath, err)
		return pullSide{}, false
	}
	info, err := os.Stat(localPath)
	if err != nil {
		if os.IsNotExist(err) {
			return pullSide{}, true // absent
		}
		fmt.Fprintf(os.Stderr, "warning: checking %s: %v\n", relPath, err)
		return pullSide{}, false
	}
	if info.IsDir() {
		return pullSide{}, false
	}

	if base := baseIdx.Files[relPath]; base != nil &&
		info.Size() == base.Size && base.SameMtime(info.ModTime()) {
		// Unchanged since last sync: reuse the recorded hash (same fast path
		// DetectChanges uses) instead of re-reading the file. SameMtime is
		// nanosecond-precise, so a same-size edit within the same second is
		// still detected and the planner refuses instead of clobbering it.
		return pullSide{Present: true, Hash: base.Hash}, true
	}
	hash, err := index.FileHash(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: hashing %s: %v\n", relPath, err)
		return pullSide{}, false
	}
	return pullSide{Present: true, Hash: hash}, true
}

// scanCandidates appends remote files that are in no index (found by walking
// the server) so they can be planned like any other path.
func scanCandidates(discovered []string, baseIdx, remoteIdx *index.Index) []string {
	var extra []string
	for _, p := range discovered {
		if baseIdx.Files[p] == nil && remoteIdx.Files[p] == nil {
			extra = append(extra, p)
		}
	}
	return extra
}
