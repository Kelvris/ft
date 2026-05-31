package transport

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

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

	sshConfig := &ssh.ClientConfig{
		User:            t.remote.Username,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         15 * time.Second,
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

	dst, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", localPath, err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("downloading %s: %w", remoteRelPath, err)
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
		stat, statErr := t.client.Stat(p)
		if statErr != nil || stat.IsDir() {
			return t.client.RemoveDirectory(p)
		}
		return fmt.Errorf("deleting %s: %w", remoteRelPath, err)
	}
	return nil
}

func (t *sftpTransport) FileExists(remoteRelPath string) (bool, int64, error) {
	stat, err := t.client.Stat(t.remotePath(remoteRelPath))
	if err != nil {
		return false, 0, nil
	}
	return true, stat.Size(), nil
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

func (t *sftpTransport) WalkDir(remoteDir string, fn func(path string, info os.FileInfo) error) error {
	walker := t.client.Walk(t.remotePath(remoteDir))
	for walker.Step() {
		if walker.Err() != nil {
			continue
		}
		rel, err := filepath.Rel(t.remotePath(remoteDir), walker.Path())
		if err != nil {
			continue
		}
		if err := fn(rel, walker.Stat()); err != nil {
			return err
		}
	}
	return nil
}
