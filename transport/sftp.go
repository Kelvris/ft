package transport

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/Kelvris/ft/config"
)

type sftpTransport struct {
	client  *sftp.Client
	sshConn *ssh.Client
	remote  *config.Remote
}

func newSFTPTransport(remote *config.Remote) (*sftpTransport, error) {
	return &sftpTransport{remote: remote}, nil
}

func (t *sftpTransport) Connect() error {
	addr := fmt.Sprintf("%s:%d", t.remote.Host, t.remote.Port)

	hostKeyCallback, knownHostsPath, err := knownHostsCallback()
	if err != nil {
		return err
	}
	sshConfig := &ssh.ClientConfig{
		User: t.remote.Username,
		HostKeyCallback: trustedHostKeyCallback(
			hostKeyCallback, knownHostsPath,
			interactiveHostKeyPrompt(os.Stdout, os.Stdin, knownHostsPath)),
		Timeout: 15 * time.Second,
	}

	authMethods := t.buildAuthMethods()
	sshConfig.Auth = authMethods

	conn, err := ssh.Dial("tcp", addr, sshConfig)
	if err != nil {
		return fmt.Errorf("SSH connect to %s: %w", addr, err)
	}
	t.sshConn = conn

	client, err := sftp.NewClient(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("SFTP init: %w", err)
	}
	t.client = client

	if t.remote.RemotePath != "" && t.remote.RemotePath != "/" {
		_ = client.MkdirAll(t.remote.RemotePath)
	}

	return nil
}

// knownHostsCallback loads ~/.ssh/known_hosts for host-key verification and
// also returns its path (so unknown hosts can be appended after the user
// confirms them). A missing file is not an error: every host is then simply
// unknown and gets the interactive trust prompt on first connect.
func knownHostsCallback() (ssh.HostKeyCallback, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, "", fmt.Errorf("locating home directory for SSH host verification: %w", err)
	}
	knownHostsPath := filepath.Join(home, ".ssh", "known_hosts")
	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		if os.IsNotExist(err) {
			callback = func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				return &knownhosts.KeyError{}
			}
			return callback, knownHostsPath, nil
		}
		return nil, "", fmt.Errorf("loading SSH known hosts from %s: %w (connect once with ssh or add the host key first)", knownHostsPath, err)
	}
	return callback, knownHostsPath, nil
}

func (t *sftpTransport) buildAuthMethods() []ssh.AuthMethod {
	var methods []ssh.AuthMethod

	if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
		if conn, err := net.Dial("unix", sock); err == nil {
			ag := agent.NewClient(conn)
			signers, err := ag.Signers()
			if err == nil && len(signers) > 0 {
				methods = append(methods, ssh.PublicKeys(signers...))
			}
			conn.Close()
		}
	}

	if t.remote.KeyPath != "" {
		if signer, err := t.loadKey(t.remote.KeyPath); err == nil {
			methods = append(methods, ssh.PublicKeys(signer))
		}
	} else {
		home, _ := os.UserHomeDir()
		for _, keyPath := range []string{
			filepath.Join(home, ".ssh", "id_rsa"),
			filepath.Join(home, ".ssh", "id_ed25519"),
			filepath.Join(home, ".ssh", "id_ecdsa"),
		} {
			if signer, err := t.loadKey(keyPath); err == nil {
				methods = append(methods, ssh.PublicKeys(signer))
				break
			}
		}
	}

	if t.remote.Password != "" {
		methods = append(methods, ssh.Password(t.remote.Password))
	}

	return methods
}

func (t *sftpTransport) loadKey(keyPath string) (ssh.Signer, error) {
	key, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	return ssh.ParsePrivateKey(key)
}

func (t *sftpTransport) Close() error {
	if t.client != nil {
		t.client.Close()
	}
	if t.sshConn != nil {
		return t.sshConn.Close()
	}
	return nil
}

func (t *sftpTransport) remotePath(rel string) string {
	// Absolute paths are used by the setup browser. Relative sync paths are
	// resolved below the configured remote root.
	if path.IsAbs(rel) {
		return path.Clean(rel)
	}
	base := t.remote.RemotePath
	if base == "" || base == "/" {
		return path.Join("/", rel)
	}
	return path.Join(base, rel)
}

func (t *sftpTransport) Upload(localPath, remoteRelPath string, progress io.Writer) error {
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer f.Close()

	parent := path.Dir(remoteRelPath)
	if parent != "." {
		if err := t.client.MkdirAll(t.remotePath(parent)); err != nil {
			return fmt.Errorf("creating remote dir %s: %w", parent, err)
		}
	}

	remoteFile := t.remotePath(remoteRelPath)
	dst, err := t.client.Create(remoteFile)
	if err != nil {
		return fmt.Errorf("creating remote %s: %w", remoteRelPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, f); err != nil {
		return fmt.Errorf("uploading %s: %w", remoteRelPath, err)
	}

	if progress != nil {
		fmt.Fprintf(progress, "uploaded  %s\n", remoteRelPath)
	}
	return nil
}

func (t *sftpTransport) Download(remoteRelPath, localPath string, progress io.Writer) error {
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}

	remoteFile := t.remotePath(remoteRelPath)
	src, err := t.client.Open(remoteFile)
	if err != nil {
		return fmt.Errorf("opening remote %s: %w", remoteRelPath, err)
	}
	defer src.Close()

	dst, err := os.CreateTemp(filepath.Dir(localPath), ".ft-download-*")
	if err != nil {
		return fmt.Errorf("creating temporary file for %s: %w", localPath, err)
	}
	tmpPath := dst.Name()
	defer os.Remove(tmpPath)

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("downloading %s: %w", remoteRelPath, err)
	}
	if err := dst.Close(); err != nil {
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

func (t *sftpTransport) List(remoteDir string) ([]string, error) {
	entries, err := t.client.ReadDir(t.remotePath(remoteDir))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func (t *sftpTransport) ListDir(remoteDir string) ([]DirEntry, error) {
	entries, err := t.client.ReadDir(t.remotePath(remoteDir))
	if err != nil {
		return nil, err
	}
	var result []DirEntry
	for _, e := range entries {
		result = append(result, DirEntry{
			Name:  e.Name(),
			IsDir: e.IsDir(),
		})
	}
	return result, nil
}

func (t *sftpTransport) Delete(remoteRelPath string) error {
	p := t.remotePath(remoteRelPath)
	if err := t.client.Remove(p); err != nil {
		// Check if it's a directory
		stat, statErr := t.client.Stat(p)
		if statErr != nil {
			// File doesn't exist or can't be stat'd - return original error
			return fmt.Errorf("deleting %s: %w", remoteRelPath, err)
		}
		if stat.IsDir() {
			if err := t.client.RemoveDirectory(p); err != nil {
				return fmt.Errorf("removing directory %s: %w", remoteRelPath, err)
			}
			return nil
		}
		return fmt.Errorf("deleting %s: %w", remoteRelPath, err)
	}
	return nil
}

// Stat reports the actual server-side state of a path. It returns an error
// wrapping fs.ErrNotExist when the path does not exist.
func (t *sftpTransport) Stat(remoteRelPath string) (RemoteInfo, error) {
	fi, err := t.client.Stat(t.remotePath(remoteRelPath))
	if err != nil {
		if os.IsNotExist(err) {
			return RemoteInfo{}, fmt.Errorf("stat %s: %w", remoteRelPath, fs.ErrNotExist)
		}
		return RemoteInfo{}, fmt.Errorf("stat %s: %w", remoteRelPath, err)
	}
	return RemoteInfo{
		Size:  fi.Size(),
		Mtime: fi.ModTime(),
		IsDir: fi.IsDir(),
	}, nil
}

func (t *sftpTransport) FileExists(remoteRelPath string) (bool, int64, error) {
	info, err := t.Stat(remoteRelPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, 0, nil
		}
		return false, 0, err
	}
	return true, info.Size, nil
}

func (t *sftpTransport) ReadFile(remoteRelPath string) ([]byte, error) {
	remoteFile := t.remotePath(remoteRelPath)
	src, err := t.client.Open(remoteFile)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, src); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (t *sftpTransport) WriteFile(remoteRelPath string, data []byte) error {
	parent := path.Dir(remoteRelPath)
	if parent != "." {
		if err := t.client.MkdirAll(t.remotePath(parent)); err != nil {
			return err
		}
	}

	remoteFile := t.remotePath(remoteRelPath)
	dst, err := t.client.Create(remoteFile)
	if err != nil {
		return err
	}
	defer dst.Close()

	_, err = dst.Write(data)
	return err
}

func (t *sftpTransport) EnsureDir(remoteRelPath string) error {
	return t.client.MkdirAll(t.remotePath(remoteRelPath))
}
