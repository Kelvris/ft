package cmd

import (
	"path/filepath"
	"testing"

	"github.com/Kelvris/ft/index"
)

const (
	hBase   = "base-hash-0000000000"
	hLocal  = "local-hash-1111111111"
	hRemote = "remote-hash-2222222222"
)

func side(present bool, hash string) pullSide {
	return pullSide{Present: present, Hash: hash}
}

// TestPlanPullMatrix locks down the git-like decision matrix of
// PULL_FIX_PLAN.md §2 (all 10 rows, plus --force/--no-delete variants and
// unknown remote hashes).
func TestPlanPullMatrix(t *testing.T) {
	tests := []struct {
		name                string
		base, local, remote pullSide
		opts                planOptions
		want                pullAction
	}{
		// Row 1 — up to date
		{"row1 all agree", side(true, hBase), side(true, hBase), side(true, hBase), planOptions{}, actNone},
		{"row1 nothing anywhere", side(false, ""), side(false, ""), side(false, ""), planOptions{}, actNone},

		// Row 2 — local clean, remote moved → fast-forward (or upstream delete)
		{"row2 fast-forward", side(true, hBase), side(true, hBase), side(true, hRemote), planOptions{}, actDownload},
		{"row2 remote hash unknown counts as changed", side(true, hBase), side(true, hBase), side(true, ""), planOptions{}, actDownload},
		{"row2 upstream delete removes local", side(true, hBase), side(true, hBase), side(false, ""), planOptions{}, actDeleteLocal},
		{"row2 --no-delete keeps local file", side(true, hBase), side(true, hBase), side(false, ""), planOptions{NoDelete: true}, actLeaveAlone},

		// Row 3 — local-only change, server untouched
		{"row3 local-only change", side(true, hBase), side(true, hLocal), side(true, hBase), planOptions{}, actLeaveAlone},

		// Row 4 — diverged
		{"row4 both changed identically", side(true, hBase), side(true, hLocal), side(true, hLocal), planOptions{}, actAdvanceBase},
		{"row4 diverged refuses", side(true, hBase), side(true, hLocal), side(true, hRemote), planOptions{}, actConflict},
		{"row4 diverged --force takes remote", side(true, hBase), side(true, hLocal), side(true, hRemote), planOptions{Force: true}, actDownload},
		{"row4 remote unknown + local dirty refuses", side(true, hBase), side(true, hLocal), side(true, ""), planOptions{}, actConflict},
		{"row4 local modified + upstream deleted", side(true, hBase), side(true, hLocal), side(false, ""), planOptions{}, actConflict},
		{"row4 force: remote deletion wins", side(true, hBase), side(true, hLocal), side(false, ""), planOptions{Force: true}, actDeleteLocal},

		// Row 5 — local delete, upstream unchanged → stays deleted
		{"row5 local delete stays deleted", side(true, hBase), side(false, ""), side(true, hBase), planOptions{}, actLeaveAlone},

		// Row 6 — modify/delete conflict
		{"row6 modify/delete conflict", side(true, hBase), side(false, ""), side(true, hRemote), planOptions{}, actConflict},
		{"row6 --force restores remote", side(true, hBase), side(false, ""), side(true, hRemote), planOptions{Force: true}, actDownload},

		// Row 7 — new upstream file
		{"row7 new upstream file", side(false, ""), side(false, ""), side(true, hRemote), planOptions{}, actDownload},

		// Row 8 — both created
		{"row8 created identically", side(false, ""), side(true, hLocal), side(true, hLocal), planOptions{}, actAdvanceBase},
		{"row8 created differently", side(false, ""), side(true, hLocal), side(true, hRemote), planOptions{}, actConflict},
		{"row8 created differently --force", side(false, ""), side(true, hLocal), side(true, hRemote), planOptions{Force: true}, actDownload},

		// Row 9 — untracked local file: never absorbed, never touched
		{"row9 untracked local", side(false, ""), side(true, hLocal), side(false, ""), planOptions{}, actLeaveAlone},

		// Row 10 — deleted on both sides: drop the sync-point entry only
		{"row10 deleted both sides", side(true, hBase), side(false, ""), side(false, ""), planOptions{}, actAdvanceBase},

		// --force must imply a backup path: every forced overwrite/delete.
		{"force never yields a silent conflict", side(true, hBase), side(true, hLocal), side(true, hRemote), planOptions{Force: true}, actDownload},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := planPull(tt.base, tt.local, tt.remote, tt.opts)
			if got.Action != tt.want {
				t.Fatalf("planPull(%+v, %+v, %+v, %+v) = %s (%s), want %s",
					tt.base, tt.local, tt.remote, tt.opts, got.Action, got.Reason, tt.want)
			}
		})
	}
}

// TestPlanPullConflictCarriesHashes ensures the conflict report has the data
// it needs to print the git-style three-way report.
func TestPlanPullConflictCarriesHashes(t *testing.T) {
	plan := planPull(side(true, hBase), side(true, hLocal), side(true, hRemote), planOptions{})
	if plan.Action != actConflict {
		t.Fatalf("want conflict, got %s", plan.Action)
	}
	if plan.BaseHash != hBase || plan.LocalHash != hLocal || plan.RemoteHash != hRemote {
		t.Fatalf("conflict lost hash context: %+v", plan)
	}
	if !plan.BasePresent || !plan.LocalPresent || !plan.RemotePresent {
		t.Fatalf("conflict lost presence context: %+v", plan)
	}
}

// TestPullCandidatesFilters covers ignored paths, command-line pathspecs and
// --include patterns — including the F10 bug where '*.php' never matched
// 'admin/index.php'.
func TestPullCandidatesFilters(t *testing.T) {
	base := index.New()
	for _, p := range []string{"admin/index.php", "index.html", "secret.env"} {
		base.Files[p] = &index.FileEntry{Hash: p}
	}
	remote := index.New()
	for _, p := range []string{"admin/index.php", "admin/config.php", "index.html"} {
		remote.Files[p] = &index.FileEntry{Hash: p}
	}

	origInclude, origScan := pullInclude, pullScan
	t.Cleanup(func() { pullInclude, pullScan = origInclude, origScan })
	pullScan = false

	// ignore + no filters: everything except the ignored path
	pullInclude = nil
	got := pullCandidates(base, remote, nil, nil, []string{"secret.env"})
	if stringsContain(got, "secret.env") {
		t.Errorf("ignored path leaked into candidates: %v", got)
	}
	if len(got) != 3 {
		t.Errorf("want 3 candidates (union minus ignored), got %d: %v", len(got), got)
	}

	// --include '*.php' must select nested paths (F10)
	pullInclude = []string{"*.php"}
	got = pullCandidates(base, remote, nil, nil, nil)
	if stringsContain(got, "index.html") || !stringsContain(got, "admin/index.php") || !stringsContain(got, "admin/config.php") {
		t.Errorf("--include '*.php' selected wrong set: %v", got)
	}

	// command-line pathspec selects a directory subtree
	pullInclude = nil
	got = pullCandidates(base, remote, nil, []string{"admin"}, nil)
	if len(got) != 2 || !stringsContain(got, "admin/config.php") {
		t.Errorf("pathspec 'admin' selected wrong set: %v", got)
	}

	// empty result set
	pullInclude = []string{"*.nope"}
	got = pullCandidates(base, remote, nil, nil, nil)
	if len(got) != 0 {
		t.Errorf("want no candidates, got %v", got)
	}
}

// TestResolveRemoteHash covers the F4/F8 observation rules: trust the last
// observation while it still matches the server, trust the remote index only
// on first sight, and report "unknown" otherwise (conservative ⇒ download).
func TestResolveRemoteHash(t *testing.T) {
	agreeing := &index.FileEntry{Hash: hBase, Size: 100, Mtime: 1}  // index matches our observation
	fresh := &index.FileEntry{Hash: hRemote, Size: 100, Mtime: 999} // someone pushed since
	unchanged := &remoteFileState{Size: 100, Mtime: 50, Hash: hBase}
	stale := &remoteFileState{Size: 90, Mtime: 40, Hash: hBase}

	if got := resolveRemoteHash(unchanged, agreeing, 100, 50); got != hBase {
		t.Errorf("unchanged observation with an agreeing index should keep its hash, got %q", got)
	}
	// The index is re-synced by every push; when it contradicts a prev that
	// only matches on size+mtime, the index wins (same-second same-size edits).
	if got := resolveRemoteHash(unchanged, fresh, 100, 50); got != hRemote {
		t.Errorf("fresh index should override a contradicting observation, got %q", got)
	}
	if got := resolveRemoteHash(nil, fresh, 100, 999); got != hRemote {
		t.Errorf("first observation should trust the index hint, got %q", got)
	}
	if got := resolveRemoteHash(stale, fresh, 100, 999); got != "" {
		t.Errorf("stale observation must yield unknown (hint only trusted on first sight), got %q", got)
	}
	if got := resolveRemoteHash(nil, fresh, 101, 50); got != "" {
		t.Errorf("size mismatch must yield unknown hash, got %q", got)
	}
	if got := resolveRemoteHash(nil, nil, 100, 50); got != "" {
		t.Errorf("no hint must yield unknown hash, got %q", got)
	}
}

// TestRemoteStateMatches pins the size/mtime comparison, including servers
// that cannot report an mtime (0 ⇒ fall back to size only).
func TestRemoteStateMatches(t *testing.T) {
	prev := &remoteFileState{Size: 10, Mtime: 20, Hash: hBase}
	if !remoteStateMatches(prev, 10, 20) {
		t.Error("identical observation should match")
	}
	if remoteStateMatches(prev, 10, 21) {
		t.Error("mtime change must be detected")
	}
	if remoteStateMatches(prev, 11, 20) {
		t.Error("size change must be detected")
	}
	if !remoteStateMatches(&remoteFileState{Size: 10, Mtime: 0, Hash: hBase}, 10, 999) {
		t.Error("unknown server mtime should fall back to size-only comparison")
	}
	if remoteStateMatches(nil, 10, 20) {
		t.Error("missing record must not match")
	}
}

// TestIsTransientPullError locks F6's retry policy: permanent FTP errors are
// never retried, transient ones are.
func TestIsTransientPullError(t *testing.T) {
	permanent := []string{
		"downloading index.html: 550 No such file or directory.",
		"permission denied",
		"no such file",
	}
	for _, msg := range permanent {
		if isTransientPullError(errString(msg)) {
			t.Errorf("%q must be permanent (no retry)", msg)
		}
	}
	transient := []string{
		"i/o timeout",
		"connection reset by peer",
		"unexpected EOF",
	}
	for _, msg := range transient {
		if !isTransientPullError(errString(msg)) {
			t.Errorf("%q must be transient (retry)", msg)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func stringsContain(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestMatchFileArgsPathspec(t *testing.T) {
	if !matchFileArgs("admin/index.php", []string{"admin"}) {
		t.Error("directory pathspec should select its subtree")
	}
	if matchFileArgs("other/index.php", []string{"admin"}) {
		t.Error("pathspec must not match siblings")
	}
	if !matchFileArgs(filepath.ToSlash("admin/index.php"), []string{filepath.FromSlash("admin/index.php")}) {
		t.Error("exact file pathspec should match")
	}
}
