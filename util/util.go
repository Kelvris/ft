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

func IsHiddenOrMeta(name string) bool {
	return strings.HasPrefix(name, ".") || name == FtDir
}
