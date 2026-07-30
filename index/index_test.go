package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectChangesDoesNotDeleteNewlyIgnoredTrackedFile(t *testing.T) {
	root := t.TempDir()
	oldWorkingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWorkingDir) })
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(root, ".ft"), 0755); err != nil {
		t.Fatal(err)
	}
	idx := New()
	idx.Files["private.env"] = &FileEntry{Hash: "previous"}
	if err := idx.Save(); err != nil {
		t.Fatal(err)
	}

	changes, err := DetectChanges(".", []string{"private.env"})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 0 {
		t.Fatalf("ignored tracked file produced changes: %#v", changes)
	}
}
