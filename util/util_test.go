package util

import (
	"path/filepath"
	"testing"
)

func TestLocalPath(t *testing.T) {
	root := t.TempDir()

	got, err := LocalPath(root, "assets/site.css")
	if err != nil {
		t.Fatalf("LocalPath returned an error: %v", err)
	}
	want := filepath.Join(root, "assets", "site.css")
	if got != want {
		t.Fatalf("LocalPath = %q, want %q", got, want)
	}

	for _, unsafe := range []string{"", ".", "..", "../secret", "assets/../../secret", "/tmp/secret"} {
		t.Run(unsafe, func(t *testing.T) {
			if _, err := LocalPath(root, unsafe); err == nil {
				t.Fatalf("LocalPath(%q) unexpectedly succeeded", unsafe)
			}
		})
	}
}
