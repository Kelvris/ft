package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

type LogEntry struct {
	Timestamp string   `json:"timestamp"`
	Action    string   `json:"action"`
	Remote    string   `json:"remote"`
	Files     []string `json:"files"`
	Status    string   `json:"status"`
}

var logCmd = &cobra.Command{
	Use:   "log",
	Short: "Show sync history",
	Long:  `Shows a chronological log of past push and pull operations.`,
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := filepath.Join(".ft", "log.json")
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Println("no sync history yet")
				return nil
			}
			return fmt.Errorf("reading log: %w", err)
		}

		var entries []LogEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return fmt.Errorf("parsing log: %w", err)
		}

		if len(entries) == 0 {
			fmt.Println("no sync history yet")
			return nil
		}

		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			ts, err := time.Parse(time.RFC3339, e.Timestamp)
			if err != nil {
				ts = time.Now()
			}
			nFiles := len(e.Files)
			fmt.Printf("%s  %s %s  (%d file%s) [%s]\n",
				ts.Format("2006-01-02 15:04:05"),
				e.Action, e.Remote,
				nFiles, plural(nFiles),
				e.Status)
		}
		return nil
	},
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func appendLog(action, remote string, files []string, status string) error {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Action:    action,
		Remote:    remote,
		Files:     files,
		Status:    status,
	}

	path := filepath.Join(".ft", "log.json")
	var entries []LogEntry

	data, err := os.ReadFile(path)
	if err == nil {
		if parseErr := json.Unmarshal(data, &entries); parseErr != nil {
			fmt.Fprintf(os.Stderr, "warning: corrupt log file, resetting: %v\n", parseErr)
			entries = nil
		}
	}

	entries = append(entries, entry)

	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0644)
}

func init() {
	rootCmd.AddCommand(logCmd)
}
