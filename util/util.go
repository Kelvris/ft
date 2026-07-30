package util

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const FtDir = ".ft"

func FtPath(elem ...string) string {
	parts := append([]string{FtDir}, elem...)
	return filepath.Join(parts...)
}

// LocalPath converts an index path into a path below root. Remote indexes are
// untrusted input, so absolute and traversal paths must never reach filesystem
// operations.
func LocalPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", fmt.Errorf("unsafe relative path %q", rel)
	}
	normalized := filepath.FromSlash(rel)
	clean := filepath.Clean(normalized)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe relative path %q", rel)
	}
	full := filepath.Join(root, clean)
	check, err := filepath.Rel(root, full)
	if err != nil || check == ".." || strings.HasPrefix(check, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("unsafe relative path %q", rel)
	}
	return full, nil
}

func EnsureDir(path string) error {
	return os.MkdirAll(path, 0755)
}

func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func FileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func FormatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
