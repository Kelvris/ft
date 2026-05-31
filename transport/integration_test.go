package transport

import (
	"fmt"
	"os"
	"testing"

	"ft/config"
	"ft/index"
)

func TestConfigParsing(t *testing.T) {
	tests := []struct {
		url      string
		proto    string
		host     string
		port     int
		user     string
		pass     string
		remPath  string
		wantErr  bool
	}{
		{"sftp://user@host.com:2222/var/www", "sftp", "host.com", 2222, "user", "", "/var/www", false},
		{"ftp://u:p@ftp.example.com:21/pub", "ftp", "ftp.example.com", 21, "u", "p", "/pub", false},
		{"sftp://user@host.com/path", "sftp", "host.com", 22, "user", "", "/path", false},
		{"ftp://host.com", "ftp", "host.com", 21, "", "", "/", false},
		{"http://host.com", "", "", 0, "", "", "", true},
		{"badurl", "", "", 0, "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			r, err := config.ParseURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if r.Protocol != tt.proto {
				t.Errorf("protocol = %q, want %q", r.Protocol, tt.proto)
			}
			if r.Host != tt.host {
				t.Errorf("host = %q, want %q", r.Host, tt.host)
			}
			if r.Port != tt.port {
				t.Errorf("port = %d, want %d", r.Port, tt.port)
			}
			if r.Username != tt.user {
				t.Errorf("username = %q, want %q", r.Username, tt.user)
			}
			if r.Password != tt.pass {
				t.Errorf("password = %q, want %q", r.Password, tt.pass)
			}
			if r.RemotePath != tt.remPath {
				t.Errorf("remotePath = %q, want %q", r.RemotePath, tt.remPath)
			}
		})
	}
}

func TestIndexChangeDetection(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	os.MkdirAll(".ft", 0755)
	idx := index.New()
	idx.Save()

	os.WriteFile("existing.txt", []byte("content"), 0644)
	os.WriteFile("unchanged.txt", []byte("same"), 0644)

	changes, err := index.DetectChanges(".", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes (both added), got %d", len(changes))
	}

	idx = index.New()
	hashExisting, _ := index.FileHash("existing.txt")
	hashUnchanged, _ := index.FileHash("unchanged.txt")
	idx.Files["existing.txt"] = &index.FileEntry{Hash: hashExisting, Mtime: 0, Size: 7}
	idx.Files["unchanged.txt"] = &index.FileEntry{Hash: hashUnchanged, Mtime: 0, Size: 4}
	idx.Files["deleted.txt"] = &index.FileEntry{Hash: "oldhash", Mtime: 0, Size: 5}
	idx.Save()

	changes, err = index.DetectChanges(".", nil)
	if err != nil {
		t.Fatal(err)
	}

	changeMap := make(map[string]index.ChangeType)
	for _, c := range changes {
		changeMap[c.Path] = c.Type
	}

	if changeMap["deleted.txt"] != index.Deleted {
		t.Errorf("expected deleted.txt to be Deleted, got %v", changeMap["deleted.txt"])
	}
	if _, exists := changeMap["existing.txt"]; exists {
		t.Errorf("existing.txt should be Unchanged (not in changes), got %v", changeMap["existing.txt"])
	}
	if _, exists := changeMap["unchanged.txt"]; exists {
		t.Errorf("unchanged.txt should be Unchanged (not in changes), got %v", changeMap["unchanged.txt"])
	}
}

func TestIgnorePatterns(t *testing.T) {
	os.WriteFile(".ftignore", []byte("*.log\nnode_modules\nbuild/"), 0644)
	defer os.Remove(".ftignore")

	patterns, err := index.LoadIgnorePatterns()
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Loaded %d ignore patterns\n", len(patterns))
}

func TestBuildIndex(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	os.MkdirAll(".ft", 0755)
	os.MkdirAll("subdir", 0755)
	os.WriteFile("a.txt", []byte("aaa"), 0644)
	os.WriteFile("subdir/b.txt", []byte("bbb"), 0644)
	os.WriteFile("ignored.log", []byte("log"), 0644)

	os.WriteFile(".ftignore", []byte("*.log"), 0644)
	patterns, _ := index.LoadIgnorePatterns()

	idx, err := index.BuildIndex(".", patterns)
	if err != nil {
		t.Fatal(err)
	}

	if len(idx.Files) != 3 {
		t.Fatalf("expected 3 files (a.txt, subdir/b.txt, .ftignore), got %d: %v", len(idx.Files), idx.Files)
	}
	if _, ok := idx.Files["a.txt"]; !ok {
		t.Error("expected a.txt in index")
	}
	if _, ok := idx.Files["subdir/b.txt"]; !ok {
		t.Error("expected subdir/b.txt in index")
	}
	if _, ok := idx.Files["ignored.log"]; ok {
		t.Error("ignored.log should not be in index")
	}
	if _, ok := idx.Files[".ftignore"]; !ok {
		t.Error("expected .ftignore in index (like .gitignore in git)")
	}
}
