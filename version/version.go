package version

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ft/index"
	"ft/transport"
	"ft/util"
)

type Snapshot struct {
	Name      string `json:"name"`
	CreatedAt string `json:"created_at"`
}

func VersionsDir() string {
	return util.FtPath("versions")
}

func VersionPath(name string, elem ...string) string {
	parts := append([]string{VersionsDir(), name}, elem...)
	return filepath.Join(parts...)
}

func DeletedFilesDir(name string) string {
	return VersionPath(name, "deleted")
}

func ListLocal() ([]Snapshot, error) {
	dir := VersionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var snapshots []Snapshot
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		idxPath := filepath.Join(dir, e.Name(), "index.json")
		if _, err := os.Stat(idxPath); os.IsNotExist(err) {
			continue
		}
		data, err := os.ReadFile(idxPath)
		if err != nil {
			continue
		}
		var snap Snapshot
		json.Unmarshal(data, &snap)
		snap.Name = e.Name()
		snapshots = append(snapshots, snap)
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Name > snapshots[j].Name
	})
	return snapshots, nil
}

func ListRemote(t transport.Transport) ([]string, error) {
	entries, err := t.ListDir(".ft/versions")
	if err != nil {
		return nil, nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir {
			names = append(names, e.Name)
		}
	}
	sort.Slice(names, func(i, j int) bool {
		return names[i] > names[j]
	})
	return names, nil
}

func Save(name string) error {
	idx, err := index.Load()
	if err != nil {
		return fmt.Errorf("loading index: %w", err)
	}

	snap := Snapshot{Name: name, CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	idxData, err := json.MarshalIndent(map[string]any{
		"name":       snap.Name,
		"created_at": snap.CreatedAt,
		"index":      idx,
	}, "", "  ")
	if err != nil {
		return err
	}

	dir := VersionPath(name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), idxData, 0644)
}

func LoadVersionIndex(name string) (*index.Index, error) {
	paths := []string{
		VersionPath(name, "index.json"),
	}
	var data []byte
	var err error
	for _, p := range paths {
		data, err = os.ReadFile(p)
		if err == nil {
			break
		}
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("version %q not found locally", name)
		}
		return nil, err
	}

	var container struct {
		Index *index.Index `json:"index"`
	}
	if err := json.Unmarshal(data, &container); err != nil {
		var idx index.Index
		if err2 := json.Unmarshal(data, &idx); err2 != nil {
			return nil, fmt.Errorf("parsing version index: %w", err)
		}
		if idx.Files == nil {
			idx.Files = make(map[string]*index.FileEntry)
		}
		return &idx, nil
	}
	if container.Index.Files == nil {
		container.Index.Files = make(map[string]*index.FileEntry)
	}
	return container.Index, nil
}

func Revert(name string, t transport.Transport, remotePath string) error {
	verIdx, err := LoadVersionIndex(name)
	if err != nil {
		return err
	}

	var restored, skipped, failed int

	for path, entry := range verIdx.Files {
		localPath := filepath.FromSlash(path)

		localHash, err := index.FileHash(localPath)
		if err == nil && localHash == entry.Hash {
			skipped++
			continue
		}

		deletedSrc := DeletedFilesDir(name)
		deletedPath := filepath.Join(deletedSrc, path)

		if _, err := os.Stat(deletedPath); err == nil {
			dest := filepath.Dir(localPath)
			os.MkdirAll(dest, 0755)
			data, _ := os.ReadFile(deletedPath)
			os.WriteFile(localPath, data, 0644)
			restored++
			continue
		}

		if t != nil {
			dest := filepath.Dir(localPath)
			os.MkdirAll(dest, 0755)
			if err := t.Download(path, localPath, nil); err == nil {
				restored++
				continue
			}
		}

		failed++
	}

	idx, err := index.New(), nil
	*idx = *verIdx
	if err := idx.Save(); err != nil {
		return fmt.Errorf("saving reverted index: %w", err)
	}

	fmt.Printf("reverted to %q: %d restored, %d skipped, %d failed\n",
		name, restored, skipped, failed)
	return nil
}

func BackupDeletedFiles(name string, paths []string, t transport.Transport) error {
	dir := DeletedFilesDir(name)
	for _, p := range paths {
		dest := filepath.Join(dir, p)
		os.MkdirAll(filepath.Dir(dest), 0755)
		if err := t.Download(p, dest, nil); err != nil {
			fmt.Fprintf(os.Stderr, "warning: backing up deleted %s: %v\n", p, err)
		}
	}
	return nil
}

func PickVersion(versions []Snapshot) string {
	if len(versions) == 0 {
		return ""
	}

	fmt.Println("\nAvailable versions:")
	for i, v := range versions {
		fmt.Printf("  [%d] %s", i+1, v.Name)
		if v.CreatedAt != "" {
			fmt.Printf("  (%s)", v.CreatedAt)
		}
		fmt.Println()
	}
	fmt.Println("  [0] Cancel")

	for {
		fmt.Print("\nSelect version to revert to: ")
		var input string
		fmt.Scanln(&input)
		input = strings.TrimSpace(input)

		if input == "" || input == "0" {
			return ""
		}

		var n int
		if _, err := fmt.Sscanf(input, "%d", &n); err == nil && n >= 1 && n <= len(versions) {
			return versions[n-1].Name
		}

		for _, v := range versions {
			if strings.EqualFold(v.Name, input) {
				return v.Name
			}
		}
		fmt.Printf("enter 1-%d or version name\n", len(versions))
	}
}
