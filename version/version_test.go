package version

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Kelvris/ft/index"
)

func TestValidateNameRejectsPathTraversal(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../outside", "nested/name", `nested\name`} {
		t.Run(name, func(t *testing.T) {
			if err := validateName(name); err == nil {
				t.Fatalf("validateName(%q) unexpectedly succeeded", name)
			}
		})
	}

	if err := validateName("pre-pull-20260730-120000"); err != nil {
		t.Fatalf("valid snapshot name rejected: %v", err)
	}
}

// chdirTemp points the process at a fresh working directory for the test.
func chdirTemp(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// setupTracked writes a file and records it in .ft/index.json so a version
// snapshot has something to record.
func setupTracked(t *testing.T, rel, content string) {
	t.Helper()
	chdirTemp(t)
	writeFile(t, rel, content)
	entry, err := index.EntryFromFile(rel)
	if err != nil {
		t.Fatal(err)
	}
	idx := index.New()
	idx.Files[rel] = entry
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}
}

// TestSaveWorkingTreeSnapshotsContent is the content half of F7: the backup
// must capture the bytes, not just the index entry, so Revert can undo an
// overwrite.
func TestSaveWorkingTreeSnapshotsContent(t *testing.T) {
	setupTracked(t, "admin/index.php", "PRECIOUS LOCAL WORK")

	if err := SaveWorkingTree("pre-pull-test", []string{"admin/index.php"}); err != nil {
		t.Fatalf("SaveWorkingTree: %v", err)
	}

	snap := VersionPath("pre-pull-test", "files", "admin/index.php")
	if got := readFile(t, snap); got != "PRECIOUS LOCAL WORK" {
		t.Fatalf("snapshot content = %q, want the local bytes", got)
	}

	// simulate a pull overwrite, then revert
	writeFile(t, "admin/index.php", "CLOBBERED BY PULL")
	if err := Revert("pre-pull-test", nil, ""); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got := readFile(t, "admin/index.php"); got != "PRECIOUS LOCAL WORK" {
		t.Fatalf("restored content = %q, want %q", got, "PRECIOUS LOCAL WORK")
	}
}

// TestRevertPrefersFilesCopyOverDeletedCopy pins the restore order:
// files/ (content snapshot) beats deleted/ (push-time copy).
func TestRevertPrefersFilesCopyOverDeletedCopy(t *testing.T) {
	setupTracked(t, "admin/index.php", "SNAPSHOT COPY")

	if err := SaveWorkingTree("v1", []string{"admin/index.php"}); err != nil {
		t.Fatal(err)
	}
	// a push-time copy of the same file, with different bytes
	writeFile(t, filepath.Join(DeletedFilesDir("v1"), "admin/index.php"), "PUSH COPY")
	writeFile(t, "admin/index.php", "CLOBBERED")

	if err := Revert("v1", nil, ""); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	if got := readFile(t, "admin/index.php"); got != "SNAPSHOT COPY" {
		t.Fatalf("restored %q, want files/ copy (%q)", got, "SNAPSHOT COPY")
	}
}

// TestRevertRestoresIndexEntry ensures revert also restores the tracking entry
// (the post-pull index still contains the file).
func TestRevertRestoresIndexEntry(t *testing.T) {
	setupTracked(t, "index.html", "v1 content")
	if err := SaveWorkingTree("v1", []string{"index.html"}); err != nil {
		t.Fatal(err)
	}

	// pull rewrote the index entry (different hash)
	writeFile(t, "index.html", "v2 content")
	changed, err := index.EntryFromFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	idx := index.New()
	idx.Files["index.html"] = changed
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	if err := Revert("v1", nil, ""); err != nil {
		t.Fatalf("Revert: %v", err)
	}
	after, err := index.Load()
	if err != nil {
		t.Fatal(err)
	}
	before, err := index.EntryFromFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := after.Files["index.html"]
	if !ok {
		t.Fatal("tracked entry missing after revert")
	}
	if got.Hash != before.Hash {
		t.Fatalf("index hash after revert = %s, want pre-pull hash %s", got.Hash, before.Hash)
	}
}
