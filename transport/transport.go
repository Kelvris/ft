package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
)

type DirEntry struct {
	Name  string
	IsDir bool
}

// RemoteInfo is the actual server-side state of a path (as opposed to what
// the remote .ft/index.json claims). Mtime is zero when the server cannot
// report it (some FTP servers lack MDTM).
type RemoteInfo struct {
	Size  int64
	Mtime time.Time
	IsDir bool
}

// ErrHashMismatch reports that downloaded content did not hash to the
// expected value; the local file has not been replaced in that case.
var ErrHashMismatch = errors.New("hash mismatch")

type Transport interface {
	Connect() error
	Close() error
	Upload(localPath, remoteRelPath string, progress io.Writer) error
	Download(remoteRelPath, localPath string, progress io.Writer) error
	List(remoteDir string) ([]string, error)
	ListDir(remoteDir string) ([]DirEntry, error)
	Delete(remoteRelPath string) error
	Stat(remoteRelPath string) (RemoteInfo, error)
	FileExists(remoteRelPath string) (bool, int64, error)
	ReadFile(remoteRelPath string) ([]byte, error)
	WriteFile(remoteRelPath string, data []byte) error
	EnsureDir(remoteRelPath string) error
}

// DownloadVerified downloads remoteRelPath and, when wantHash is non-empty,
// verifies the content hash *before* the file replaces localPath. On mismatch
// the local file is left untouched and ErrHashMismatch is returned.
// progress, when non-nil, is forwarded to the underlying Download (F12).
func DownloadVerified(t Transport, remoteRelPath, localPath, wantHash string, progress io.Writer) error {
	if wantHash == "" {
		return t.Download(remoteRelPath, localPath, progress)
	}

	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Stage next to the destination so the final rename stays atomic.
	staging, err := os.CreateTemp(dir, ".ft-verify-*")
	if err != nil {
		return fmt.Errorf("creating staging file for %s: %w", localPath, err)
	}
	stagingPath := staging.Name()
	staging.Close()
	defer os.Remove(stagingPath)

	if err := t.Download(remoteRelPath, stagingPath, progress); err != nil {
		return err
	}
	got, err := index.FileHash(stagingPath)
	if err != nil {
		return fmt.Errorf("hashing %s: %w", localPath, err)
	}
	if got != wantHash {
		return fmt.Errorf("%s: %w (expected %s, got %s)", remoteRelPath, ErrHashMismatch,
			shortHash(wantHash), shortHash(got))
	}
	if err := os.Rename(stagingPath, localPath); err != nil {
		return fmt.Errorf("replacing %s: %w", localPath, err)
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12] + "…"
	}
	return h
}

func NewTransport(remote *config.Remote) (Transport, error) {
	switch remote.Protocol {
	case "ftp":
		return newFTPTransport(remote)
	case "sftp":
		return newSFTPTransport(remote)
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", remote.Protocol)
	}
}

func SyncIndexToRemote(t Transport, idx *index.Index) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	if err := t.EnsureDir(".ft"); err != nil {
		return err
	}
	return t.WriteFile(".ft/index.json", data)
}

func FetchIndexFromRemote(t Transport) (*index.Index, error) {
	data, err := t.ReadFile(".ft/index.json")
	if err != nil {
		return nil, fmt.Errorf("reading remote index: %w", err)
	}
	var idx index.Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parsing remote index: %w", err)
	}
	if idx.Files == nil {
		idx.Files = make(map[string]*index.FileEntry)
	}
	return &idx, nil
}
