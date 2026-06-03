package util

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const SecretsDir = ".ft/secrets"

func SecretsPath(elem ...string) string {
	home, _ := os.UserHomeDir()
	parts := append([]string{home, SecretsDir}, elem...)
	return filepath.Join(parts...)
}

func GenerateSecretID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func SavePassword(remoteName, password string) (string, error) {
	ensureSecretsDir()
	id := GenerateSecretID()
	dir := SecretsPath(id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating secret dir: %w", err)
	}
	path := filepath.Join(dir, "password")
	if err := os.WriteFile(path, []byte(password), 0600); err != nil {
		return "", fmt.Errorf("writing password: %w", err)
	}
	if err := saveRegistry(remoteName, id); err != nil {
		return "", err
	}
	return id, nil
}

func LoadPassword(remoteName string) (string, error) {
	id, err := loadRegistry(remoteName)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", nil
	}

	path := SecretsPath(id, "password")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading password: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

func GetEnvPassword() string {
	return os.Getenv("FT_PASSWORD")
}

func HasVaultPassword(remoteName string) bool {
	id, _ := loadRegistry(remoteName)
	return id != ""
}

func RotateSecret(remoteName string) error {
	oldID, err := loadRegistry(remoteName)
	if err != nil || oldID == "" {
		return nil
	}

	oldPath := SecretsPath(oldID, "password")
	data, err := os.ReadFile(oldPath)
	if err != nil {
		return nil
	}
	password := strings.TrimSpace(string(data))
	if password == "" {
		return nil
	}

	// Shred old password file before deleting
	if exec.Command("shred", "--version").Run() == nil {
		exec.Command("shred", "-u", oldPath).Run()
	}

	newID := GenerateSecretID()
	newDir := SecretsPath(newID)
	if err := os.MkdirAll(newDir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(newDir, "password"), []byte(password), 0600); err != nil {
		return err
	}

	if err := saveRegistry(remoteName, newID); err != nil {
		return err
	}

	os.RemoveAll(SecretsPath(oldID))

	return nil
}

func ClearPassword(remoteName string) error {
	ensureSecretsDir()
	id, err := loadRegistry(remoteName)
	if err != nil {
		return err
	}
	if id != "" {
		os.RemoveAll(SecretsPath(id))
	}
	return saveRegistry(remoteName, "")
}

type registry struct {
	Remotes map[string]string `json:"remotes"`
}

func registryPath() string {
	return SecretsPath("registry.json")
}

func saveRegistry(remoteName, id string) error {
	reg := loadRegistryMap()
	reg[remoteName] = id
	return writeRegistry(reg)
}

func loadRegistry(remoteName string) (string, error) {
	reg := loadRegistryMap()
	return reg[remoteName], nil
}

func loadRegistryMap() map[string]string {
	path := registryPath()
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[string]string)
	}
	var reg registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return make(map[string]string)
	}
	if reg.Remotes == nil {
		reg.Remotes = make(map[string]string)
	}
	return reg.Remotes
}

func writeRegistry(remotes map[string]string) error {
	reg := registry{Remotes: remotes}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(registryPath())
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(registryPath(), data, 0600)
}

func ensureSecretsDir() {
	os.MkdirAll(SecretsPath(), 0700)
}
