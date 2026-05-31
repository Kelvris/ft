package transport

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"

	"ft/config"
)

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

func (t *ftpTransport) ensureRemoteDir(client *ftp.ServerConn, dir string) {
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	current := ""
	for _, part := range parts {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		_ = client.MakeDir(current)
	}
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
	resp, err := t.client.Retr(remoteFile)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", remoteRelPath, err)
	}
	defer resp.Close()

	out, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", localPath, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp); err != nil {
		return fmt.Errorf("writing %s: %w", localPath, err)
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

func (t *ftpTransport) FileExists(remoteRelPath string) (bool, int64, error) {
	size, err := t.client.FileSize(t.remotePath(remoteRelPath))
	if err != nil {
		return false, 0, nil
	}
	return true, size, nil
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
	t.ensureRemoteDir(t.client, t.remotePath(remoteRelPath))
	return nil
}
