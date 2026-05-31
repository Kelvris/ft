package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Kelvris/ft/util"
)

type Remote struct {
	Name       string `json:"-"`
	Protocol   string `json:"protocol"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`
	KeyPath    string `json:"key_path,omitempty"`
	RemotePath string `json:"remote_path"`
}

type Config struct {
	Remotes map[string]*Remote `json:"remotes"`
}

func (c *Config) Save() error {
	if err := util.EnsureDir(util.FtDir); err != nil {
		return err
	}
	path := filepath.Join(util.FtDir, "config.json")
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func LoadConfig() (*Config, error) {
	path := filepath.Join(util.FtDir, "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{Remotes: make(map[string]*Remote)}, nil
		}
		return nil, err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if cfg.Remotes == nil {
		cfg.Remotes = make(map[string]*Remote)
	}
	return &cfg, nil
}

func ParseURL(rawURL string) (*Remote, error) {
	if !strings.Contains(rawURL, "://") {
		return nil, fmt.Errorf("missing protocol in URL (use ftp:// or sftp://)")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	r := &Remote{}

	switch u.Scheme {
	case "ftp":
		r.Protocol = "ftp"
	case "sftp":
		r.Protocol = "sftp"
	default:
		return nil, fmt.Errorf("unsupported protocol %q (use ftp:// or sftp://)", u.Scheme)
	}

	r.Host = u.Hostname()
	if r.Host == "" {
		return nil, fmt.Errorf("missing host in URL")
	}

	portStr := u.Port()
	if portStr == "" {
		if r.Protocol == "ftp" {
			r.Port = 21
		} else {
			r.Port = 22
		}
	} else {
		port, err := strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid port %q", portStr)
		}
		r.Port = port
	}

	if u.User != nil {
		r.Username = u.User.Username()
		r.Password, _ = u.User.Password()
	}

	r.RemotePath = u.Path
	if r.RemotePath == "" {
		r.RemotePath = "/"
	}

	return r, nil
}
