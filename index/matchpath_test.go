package index

import (
	"os"
	"testing"
	"time"
)

// TestMatchPath locks the gitish pathspec semantics used by --include and
// --exclude (PULL_FIX_PLAN.md F10): a bare '*.php' must select
// 'admin/index.php', and 'admin/*' must select the whole subtree.
func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		// basename match when the pattern has no '/'
		{"*.php", "admin/index.php", true},
		{"*.php", "index.php", true},
		{"*.php", "index.html", false},
		{"*.php", "admin/index.txt", false},
		{"index.php", "admin/index.php", true},

		// directory subtree
		{"admin/*", "admin/index.php", true},
		{"admin/*", "admin/sub/page.php", true},
		{"admin/*", "admin", false}, // contents of admin, not admin itself
		{"admin/*", "other/x.php", false},
		{"admin/*", "adminx/y.php", false},

		// trailing slash = directory prefix
		{"admin/", "admin/index.php", true},
		{"admin/", "admin/deep/nested.txt", true},
		{"admin/", "admin", true},
		{"admin/", "administrator/x.php", false},

		// exact match
		{"admin/index.php", "admin/index.php", true},
		{"admin/index.php", "admin/other.php", false},

		// explicit glob against the full path
		{"admin/*.php", "admin/index.php", true},
		{"admin/*.php", "admin/sub/index.php", false},

		// no false positives / degenerate input
		{"", "anything.txt", false},
		{"*.css", "styles/main.css", true},
		{"*.css", "styles/main.js", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"~"+tt.path, func(t *testing.T) {
			if got := MatchPath(tt.pattern, tt.path); got != tt.want {
				t.Errorf("MatchPath(%q, %q) = %v, want %v", tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}

// TestIsIgnoredNegationUnchanged guards the .ftignore '!' behaviour, which
// MatchPath deliberately does not implement.
func TestIsIgnoredNegationUnchanged(t *testing.T) {
	patterns := []string{"*.php", "!admin/keep.php"}
	if !IsIgnored("admin/drop.php", patterns) {
		t.Error("*.php should be ignored")
	}
	if IsIgnored("admin/keep.php", patterns) {
		t.Error("! negation should un-ignore the path")
	}
	if !IsIgnored("admin/keep.php", []string{"*.php"}) {
		t.Error("without negation the path stays ignored")
	}
}

// TestDetectChangesSeesSameSecondSameSizeEdit is the regression guard for the
// racy-mtime bug: a same-size rewrite within the same second as the recorded
// entry must still be reported as Modified (PULL_FIX_PLAN.md F6 — otherwise
// `ft push` silently uploads nothing).
func TestDetectChangesSeesSameSecondSameSizeEdit(t *testing.T) {
	root := t.TempDir()
	oldWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkingDir) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	const path = "a.txt"
	if err := os.WriteFile(path, []byte("a1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	entry, err := EntryFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := New()
	idx.Files[path] = entry
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	// same size, same second as the recorded entry
	if err := os.WriteFile(path, []byte("a2\n"), 0644); err != nil {
		t.Fatal(err)
	}

	changes, err := DetectChanges(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Type != Modified {
		t.Fatalf("want one Modified change, got %+v", changes)
	}
}

// TestSameMtimeFallback pins the seconds-based fallback for entries written
// before mtime_ns existed.
func TestSameMtimeFallback(t *testing.T) {
	now := time.Now()
	legacy := &FileEntry{Mtime: now.Unix()}
	if !legacy.SameMtime(now) {
		t.Error("legacy entry should match within the same second")
	}
	if legacy.SameMtime(now.Add(2 * time.Second)) {
		t.Error("legacy entry must not match a different second")
	}

	modern := &FileEntry{Mtime: now.Unix(), MtimeNS: now.UnixNano()}
	if !modern.SameMtime(now) {
		t.Error("nanosecond entry should match exactly")
	}
	if modern.SameMtime(now.Add(time.Nanosecond)) {
		t.Error("nanosecond entry must detect a sub-second rewrite")
	}
}
