package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kelvris/ft/util"
)

type FileEntry struct {
	Hash  string `json:"hash"`
	Mtime int64  `json:"mtime"`
	Size  int64  `json:"size"`
	// MtimeNS is the same timestamp at nanosecond resolution. One-second
	// mtimes cannot tell a same-size rewrite within the same second apart
	// (git calls this "racy"), so local fast paths compare this when present.
	MtimeNS int64 `json:"mtime_ns,omitempty"`
}

// SameMtime reports whether a file's current mtime still matches what the
// entry recorded. Nanoseconds are exact; entries written before mtime_ns
// existed fall back to whole seconds.
func (e *FileEntry) SameMtime(cur time.Time) bool {
	if e.MtimeNS != 0 {
		return cur.UnixNano() == e.MtimeNS
	}
	return cur.Unix() == e.Mtime
}

type Index struct {
	Files map[string]*FileEntry `json:"files"`
}

func New() *Index {
	return &Index{
		Files: make(map[string]*FileEntry),
	}
}

func Load() (*Index, error) {
	path := filepath.Join(util.FtDir, "index.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return New(), nil
		}
		return nil, fmt.Errorf("reading index: %w", err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing index: %w", err)
	}
	if idx.Files == nil {
		idx.Files = make(map[string]*FileEntry)
	}
	return &idx, nil
}

func (idx *Index) Save() error {
	if err := util.EnsureDir(util.FtDir); err != nil {
		return err
	}
	path := filepath.Join(util.FtDir, "index.json")
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type ChangeType int

const (
	Added ChangeType = iota
	Modified
	Deleted
	Unchanged
)

func (ct ChangeType) String() string {
	switch ct {
	case Added:
		return "added"
	case Modified:
		return "modified"
	case Deleted:
		return "deleted"
	case Unchanged:
		return "unchanged"
	default:
		return "unknown"
	}
}

type Change struct {
	Path       string     `json:"path"`
	Type       ChangeType `json:"type"`
	LocalHash  string     `json:"local_hash,omitempty"`
	RemoteHash string     `json:"remote_hash,omitempty"`
}

func DetectChanges(root string, ignorePatterns []string) ([]Change, error) {
	idx, err := Load()
	if err != nil {
		return nil, err
	}

	currentFiles := make(map[string]bool)
	var changes []Change

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		relPath = filepath.ToSlash(relPath)

		if shouldSkipDir(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if isIgnored(relPath, ignorePatterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		currentFiles[relPath] = true

		entry, exists := idx.Files[relPath]
		if !exists {
			hash, err := FileHash(path)
			if err != nil {
				return err
			}
			changes = append(changes, Change{
				Path:      relPath,
				Type:      Added,
				LocalHash: hash,
			})
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.Size() != entry.Size || !entry.SameMtime(info.ModTime()) {
			hash, err := FileHash(path)
			if err != nil {
				return err
			}
			if hash != entry.Hash {
				changes = append(changes, Change{
					Path:       relPath,
					Type:       Modified,
					LocalHash:  hash,
					RemoteHash: entry.Hash,
				})
				return nil
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	for path := range idx.Files {
		if !currentFiles[path] && !shouldSkipDir(path) && !isIgnored(path, ignorePatterns) {
			changes = append(changes, Change{
				Path:       path,
				Type:       Deleted,
				RemoteHash: idx.Files[path].Hash,
			})
		}
	}

	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Path < changes[j].Path
	})

	return changes, nil
}

func BuildIndex(root string, ignorePatterns []string) (*Index, error) {
	idx := New()

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}

		if relPath == "." {
			return nil
		}

		relPath = filepath.ToSlash(relPath)

		if shouldSkipDir(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if isIgnored(relPath, ignorePatterns) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		hash, err := FileHash(path)
		if err != nil {
			return err
		}

		idx.Files[relPath] = &FileEntry{
			Hash:    hash,
			Mtime:   info.ModTime().Unix(),
			MtimeNS: info.ModTime().UnixNano(),
			Size:    info.Size(),
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return idx, nil
}

func LoadIgnorePatterns() ([]string, error) {
	names := []string{".ftignore", ".ftpignore"}
	var data []byte
	for _, name := range names {
		d, err := os.ReadFile(name)
		if err == nil {
			data = d
			break
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if data == nil {
		return nil, nil
	}
	lines := strings.Split(string(data), "\n")
	var patterns []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns, nil
}

func matchPattern(path, pattern string) bool {
	if pattern == "" {
		return false
	}

	dirPattern := strings.TrimSuffix(pattern, "/")

	if path == pattern || path == dirPattern {
		return true
	}

	if strings.HasPrefix(path, dirPattern+"/") || strings.HasPrefix(path, pattern+"/") {
		return true
	}

	base := filepath.Base(path)
	if matched, _ := filepath.Match(dirPattern, base); matched {
		return true
	}

	if matched, _ := filepath.Match(dirPattern, path); matched {
		return true
	}

	parts := strings.Split(path, "/")
	for _, part := range parts {
		if matched, _ := filepath.Match(dirPattern, part); matched {
			return true
		}
	}

	return false
}

func isIgnored(path string, patterns []string) bool {
	ignored := false
	for _, pattern := range patterns {
		if pattern == "" {
			continue
		}

		negate := strings.HasPrefix(pattern, "!")
		pat := strings.TrimPrefix(pattern, "!")
		if pat == "" {
			continue
		}

		if matchPattern(path, pat) {
			ignored = !negate
		}
	}
	return ignored
}

// IsIgnored reports whether path matches the supplied .ftignore-style patterns.
func IsIgnored(path string, patterns []string) bool {
	return isIgnored(filepath.ToSlash(path), patterns)
}

// MatchPath reports whether relPath matches pattern using gitignore-ish
// semantics: exact match, directory-prefix match, basename match when the
// pattern contains no '/', and filepath.Match against the full path for
// explicit globs. A pattern that matches a parent directory also selects
// everything below it, so 'admin/*' covers 'admin/sub/page.php'.
// It is the shared matcher for --include/--exclude filters (a bare '*.php'
// therefore matches 'admin/index.php').
// Negation ('!') is not handled here; that belongs to the ignore matcher.
func MatchPath(pattern, relPath string) bool {
	if pattern == "" {
		return false
	}
	relPath = filepath.ToSlash(relPath)
	if matchPattern(relPath, pattern) {
		return true
	}
	for dir := path.Dir(relPath); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if matchPattern(dir, pattern) {
			return true
		}
	}
	return false
}

// EntryFromFile builds a FileEntry from a file on disk (hash, size, mtime).
// It is the source of truth for index entries written by pull.
func EntryFromFile(path string) (*FileEntry, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}
	hash, err := FileHash(path)
	if err != nil {
		return nil, err
	}
	return &FileEntry{
		Hash:    hash,
		Mtime:   info.ModTime().Unix(),
		MtimeNS: info.ModTime().UnixNano(),
		Size:    info.Size(),
	}, nil
}

// Auto-excluded directories (like .ft, .git)
var skipDirs = []string{".ft", ".git"}

func shouldSkipDir(relPath string) bool {
	for _, dir := range skipDirs {
		if strings.HasPrefix(relPath, dir+"/") || relPath == dir {
			return true
		}
	}
	return false
}
