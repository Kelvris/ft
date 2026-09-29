package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/textproto"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	"github.com/Kelvris/ft/config"
)

// ftpStatusFileUnavailable is the FTP reply code used for "no such file" and
// for permission errors alike (RFC 959).
const ftpStatusFileUnavailable = 550

func ftpStatusCode(err error) int {
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code
	}
	return 0
}

type ftpTransport struct {
	client *ftp.ServerConn
	remote *config.Remote
}

func newFTPTransport(remote *config.Remote) (*ftpTransport, error) {
	return &ftpTransport{remote: remote}, nil
}

func (t *ftpTransport) Connect() error {
	addr := fmt.Sprintf("%s:%d", t.remote.Host, t.remote.Port)
	client, err := ftp.Dial(addr, ftp.DialWithTimeout(15*time.Second))
	if err != nil {
		return fmt.Errorf("FTP connect to %s: %w", addr, err)
	}

	if err := client.Login(t.remote.Username, t.remote.Password); err != nil {
		client.Quit()
		return fmt.Errorf("FTP login as %s: %w", t.remote.Username, err)
	}

	if t.remote.RemotePath != "" && t.remote.RemotePath != "/" {
		if err := client.ChangeDir(t.remote.RemotePath); err != nil {
			t.ensureRemoteDir(client, t.remote.RemotePath)
			if err := client.ChangeDir(t.remote.RemotePath); err != nil {
				client.Quit()
				return fmt.Errorf("FTP chdir to %s: %w", t.remote.RemotePath, err)
			}
		}
	}

	t.client = client
	return nil
}

func (t *ftpTransport) ensureRemoteDir(client *ftp.ServerConn, dir string) error {
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	current := ""
	for _, part := range parts {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		client.MakeDir(current) // best-effort: ignore errors (dir may already exist)
	}
	return nil
}

func (t *ftpTransport) Close() error {
	if t.client != nil {
		return t.client.Quit()
	}
	return nil
}

func (t *ftpTransport) remotePath(rel string) string {
	// We've already CD'd to the remote path during Connect,
	// so we use relative paths directly.
	if rel == "" || rel == "/" {
		return "."
	}
	return rel
}

func (t *ftpTransport) Upload(localPath, remoteRelPath string, progress io.Writer) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer f.Close()

	parent := path.Dir(remoteRelPath)
	if parent != "." {
		t.ensureRemoteDir(t.client, parent)
	}

	// Binary mode: ASCII mode silently rewrites line endings, which would make
	// content hashes disagree between local and remote.
	_ = t.client.Type(ftp.TransferTypeBinary)

	remoteFile := t.remotePath(remoteRelPath)
	if err := t.client.Stor(remoteFile, f); err != nil {
		return fmt.Errorf("uploading %s: %w", remoteRelPath, err)
	}

	if progress != nil {
		fmt.Fprintf(progress, "uploaded  %s\n", remoteRelPath)
	}
	return nil
}

func (t *ftpTransport) Download(remoteRelPath, localPath string, progress io.Writer) error {
	if err := os.MkdirAll(path.Dir(localPath), 0755); err != nil {
		return err
	}

	remoteFile := t.remotePath(remoteRelPath)
	_ = t.client.Type(ftp.TransferTypeBinary)
	resp, err := t.client.Retr(remoteFile)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", remoteRelPath, err)
	}
	defer resp.Close()

	out, err := os.CreateTemp(filepath.Dir(localPath), ".ft-download-*")
	if err != nil {
		return fmt.Errorf("creating temporary file for %s: %w", localPath, err)
	}
	tmpPath := out.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(out, resp); err != nil {
		out.Close()
		return fmt.Errorf("writing %s: %w", localPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("closing temporary file for %s: %w", localPath, err)
	}
	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("replacing %s: %w", localPath, err)
	}

	if progress != nil {
		fmt.Fprintf(progress, "downloaded  %s\n", remoteRelPath)
	}
	return nil
}

func (t *ftpTransport) List(remoteDir string) ([]string, error) {
	entries, err := t.client.List(t.remotePath(remoteDir))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name)
	}
	return names, nil
}

func (t *ftpTransport) ListDir(remoteDir string) ([]DirEntry, error) {
	entries, err := t.client.List(t.remotePath(remoteDir))
	if err != nil {
		return nil, err
	}
	var result []DirEntry
	for _, e := range entries {
		result = append(result, DirEntry{
			Name:  e.Name,
			IsDir: e.Type == ftp.EntryTypeFolder,
		})
	}
	return result, nil
}

func (t *ftpTransport) Delete(remoteRelPath string) error {
	if err := t.client.Delete(t.remotePath(remoteRelPath)); err != nil {
		return fmt.Errorf("deleting %s: %w", remoteRelPath, err)
	}
	return nil
}

// Stat reports the actual server-side state of a path. It returns an error
// wrapping fs.ErrNotExist when the path does not exist.
func (t *ftpTransport) Stat(remoteRelPath string) (RemoteInfo, error) {
	remoteFile := t.remotePath(remoteRelPath)
	var info RemoteInfo

	size, err := t.client.FileSize(remoteFile)
	if err != nil {
		if ftpStatusCode(err) == ftpStatusFileUnavailable {
			// SIZE is file-only; directories (and missing paths) land here.
			if entry, entryErr := t.client.GetEntry(remoteFile); entryErr == nil {
				info.IsDir = entry.Type == ftp.EntryTypeFolder
				info.Size = int64(entry.Size)
				info.Mtime = entry.Time
				return info, nil
			}
			return RemoteInfo{}, fmt.Errorf("stat %s: %w", remoteRelPath, fs.ErrNotExist)
		}
		return RemoteInfo{}, fmt.Errorf("stat %s: %w", remoteRelPath, err)
	}
	info.Size = size
	if t.client.IsGetTimeSupported() {
		if mtime, mtimeErr := t.client.GetTime(remoteFile); mtimeErr == nil {
			info.Mtime = mtime
		}
	}
	return info, nil
}

func (t *ftpTransport) FileExists(remoteRelPath string) (bool, int64, error) {
	info, err := t.Stat(remoteRelPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, info.Size, nil
}

func (t *ftpTransport) ReadFile(remoteRelPath string) ([]byte, error) {
	resp, err := t.client.Retr(t.remotePath(remoteRelPath))
	if err != nil {
		return nil, err
	}
	defer resp.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, resp); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (t *ftpTransport) WriteFile(remoteRelPath string, data []byte) error {
	parent := path.Dir(remoteRelPath)
	if parent != "." {
		t.ensureRemoteDir(t.client, parent)
	}

	return t.client.Stor(t.remotePath(remoteRelPath), bytes.NewReader(data))
}

func (t *ftpTransport) EnsureDir(remoteRelPath string) error {
	return t.ensureRemoteDir(t.client, t.remotePath(remoteRelPath))
}
