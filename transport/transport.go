package transport

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
)

type DirEntry struct {
	Name  string
	IsDir bool
}

type Transport interface {
	Connect() error
	Close() error
	Upload(localPath, remoteRelPath string, progress io.Writer) error
	Download(remoteRelPath, localPath string, progress io.Writer) error
	List(remoteDir string) ([]string, error)
	ListDir(remoteDir string) ([]DirEntry, error)
	Delete(remoteRelPath string) error
	FileExists(remoteRelPath string) (bool, int64, error)
	ReadFile(remoteRelPath string) ([]byte, error)
	WriteFile(remoteRelPath string, data []byte) error
	EnsureDir(remoteRelPath string) error
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
		return nil, fmt.Errorf("remote has no index (push first): %w", err)
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
