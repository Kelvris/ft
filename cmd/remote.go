package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/util"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func promptPassword() (string, error) {
	fmt.Print("Enter password: ")
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", scanner.Err()
	}
	return strings.TrimSpace(scanner.Text()), nil
}

var remoteCmd = &cobra.Command{
	Use:   "remote",
	Short: "Manage remote servers",
	Long: `List, add, or remove remote server configurations.

For interactive setup, use 'ft setup' instead.`,
}

var remoteAddCmd = &cobra.Command{
	Use:   "add <name> <url>",
	Short: "Add a remote server",
	Long: `Add a remote with a URL like:
  ftp://user:pass@host:21/path
  sftp://user@host:22/path
  sftp://user:pass@host:22/path`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		rawURL := args[1]

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if _, exists := cfg.Remotes[name]; exists {
			return fmt.Errorf("remote %q already exists", name)
		}

		remote, err := config.ParseURL(rawURL)
		if err != nil {
			return err
		}
		remote.Name = name
		cfg.Remotes[name] = remote

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		fmt.Printf("added remote: %s -> %s://%s:%d%s\n",
			name, remote.Protocol, remote.Host, remote.Port, remote.RemotePath)
		return nil
	},
}

var remoteLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List configured remotes",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if len(cfg.Remotes) == 0 {
			fmt.Println("no remotes configured")
			return nil
		}

		for name, r := range cfg.Remotes {
			passDisplay := ""
			if r.Password != "" {
				passDisplay = ":***"
			}
			fmt.Printf("%s\t%s://%s%s@%s:%d%s\n",
				name, r.Protocol, r.Username, passDisplay, r.Host, r.Port, r.RemotePath)
		}
		return nil
	},
}

var remoteRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a remote",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}

		if _, exists := cfg.Remotes[name]; !exists {
			return fmt.Errorf("remote %q not found", name)
		}

		delete(cfg.Remotes, name)
		if err := cfg.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		fmt.Printf("removed remote %q\n", name)
		return nil
	},
}

var remoteSetPasswordCmd = &cobra.Command{
	Use:   "set-password <name>",
	Short: "Set saved password for a remote (stored in rotating vault)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		if _, exists := cfg.Remotes[name]; !exists {
			return fmt.Errorf("remote %q not found", name)
		}

		fmt.Print("Enter password: ")
		passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
		password := string(passwordBytes)

		id, err := util.SavePassword(name, password)
		if err != nil {
			return fmt.Errorf("saving password: %w", err)
		}
		fmt.Printf("password saved for remote %q (vault id: %s)\n", name, id)
		return nil
	},
}

var remoteClearPasswordCmd = &cobra.Command{
	Use:   "clear-password <name>",
	Short: "Remove saved password for a remote",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if err := util.ClearPassword(name); err != nil {
			return fmt.Errorf("clearing password: %w", err)
		}
		fmt.Printf("password cleared for remote %q\n", name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(remoteCmd)
	remoteCmd.AddCommand(remoteAddCmd)
	remoteCmd.AddCommand(remoteLsCmd)
	remoteCmd.AddCommand(remoteRmCmd)
	remoteCmd.AddCommand(remoteSetPasswordCmd)
	remoteCmd.AddCommand(remoteClearPasswordCmd)
}
