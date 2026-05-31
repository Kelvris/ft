package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/index"
	"github.com/Kelvris/ft/transport"
	"github.com/Kelvris/ft/util"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive remote configuration wizard",
	Long: `Walks you through connecting to an FTP/SFTP server,
browsing directories, and saving the remote configuration.

Creates .ft/ if it doesn't exist. No arguments needed — just run:

  ft setup`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		util.EnsureDir(util.FtDir)

		cfg, err := config.LoadConfig()
		if err != nil {
			cfg = &config.Config{Remotes: make(map[string]*config.Remote)}
		}
		if cfg.Remotes == nil {
			cfg.Remotes = make(map[string]*config.Remote)
		}

		protocol := promptChoice("Protocol", []string{"sftp", "ftp"}, "sftp")
		host := promptInput("Host", "")
		defaultPort := map[string]string{"sftp": "22", "ftp": "21"}[protocol]
		portStr := promptInput("Port", defaultPort)
		port, _ := strconv.Atoi(portStr)
		if port == 0 {
			port, _ = strconv.Atoi(defaultPort)
		}
		username := promptInput("Username", "")

		fmt.Printf("Password: ")
		passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return fmt.Errorf("reading password: %w", err)
		}
		password := string(passwordBytes)

		if host == "" || username == "" {
			return fmt.Errorf("host and username are required")
		}

		fmt.Printf("\nConnecting to %s://%s@%s:%d ...\n", protocol, username, host, port)

		remote := &config.Remote{
			Protocol:   protocol,
			Host:       host,
			Port:       port,
			Username:   username,
			Password:   password,
			RemotePath: "/",
		}
		remote.Name = "origin"

		t, err := transport.NewTransport(remote)
		if err != nil {
			return fmt.Errorf("creating transport: %w", err)
		}
		if err := t.Connect(); err != nil {
			return fmt.Errorf("connection failed: %w", err)
		}
		fmt.Println("Connected!")
		fmt.Println()

		chosenPath, err := browseDirectories(t, "/")
		t.Close()
		if err != nil {
			return err
		}
		if chosenPath == "" {
			fmt.Println("cancelled")
			return nil
		}

		remote.RemotePath = chosenPath
		cfg.Remotes["origin"] = remote

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		if password != "" {
			if _, err := util.SavePassword("origin", password); err != nil {
				return fmt.Errorf("saving password: %w", err)
			}
			remote.Password = ""
			if err := cfg.Save(); err != nil {
				return fmt.Errorf("saving config: %w", err)
			}
		}

		idx := index.New()
		if err := idx.Save(); err != nil {
			return fmt.Errorf("saving index: %w", err)
		}

		fmt.Printf("\nRemote \"origin\" configured: %s://%s@%s:%d%s\n",
			protocol, username, host, port, chosenPath)
		fmt.Println("You can now run: ft push")
		return nil
	},
}

func promptInput(label, defaultValue string) string {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		if defaultValue != "" {
			fmt.Printf("%s [%s]: ", label, defaultValue)
		} else {
			fmt.Printf("%s: ", label)
		}
		if !scanner.Scan() {
			return defaultValue
		}
		val := strings.TrimSpace(scanner.Text())
		if val == "" {
			return defaultValue
		}
		return val
	}
}

func promptChoice(label string, options []string, defaultVal string) string {
	opts := make([]string, len(options))
	for i, o := range options {
		mark := " "
		if o == defaultVal {
			mark = "*"
		}
		opts[i] = fmt.Sprintf("  %s %s", mark, o)
	}
	fmt.Printf("%s:\n%s\n", label, strings.Join(opts, "\n"))

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("Enter choice [%s]: ", defaultVal)
		if !scanner.Scan() {
			return defaultVal
		}
		val := strings.TrimSpace(scanner.Text())
		if val == "" {
			return defaultVal
		}
		for _, o := range options {
			if strings.EqualFold(val, o) {
				return o
			}
		}
		fmt.Printf("Invalid. Options: %s\n", strings.Join(options, ", "))
	}
}

func browseDirectories(t transport.Transport, currentPath string) (string, error) {
	scanner := bufio.NewScanner(os.Stdin)

	for {
		entries, err := t.ListDir(currentPath)
		if err != nil {
			return "", fmt.Errorf("listing %s: %w", currentPath, err)
		}

		dirs := []transport.DirEntry{}
		files := []transport.DirEntry{}
		for _, e := range entries {
			if strings.HasPrefix(e.Name, ".") {
				continue
			}
			if e.IsDir {
				dirs = append(dirs, e)
			} else {
				files = append(files, e)
			}
		}

		display := currentPath
		if display == "" {
			display = "/"
		}
		fmt.Printf("Contents of %s:\n", display)
		fmt.Println("  [0] Use this directory")
		if currentPath != "/" {
			fmt.Println("  [..] Go up")
		}
		if len(dirs) == 0 && len(files) == 0 {
			fmt.Println("  (empty)")
		} else {
			for i, d := range dirs {
				fmt.Printf("  [%d] /%s/\n", i+1, d.Name)
			}
			for i, f := range files {
				fmt.Printf("  [%d]  %s\n", len(dirs)+i+1, f.Name)
			}
		}

		fmt.Print("\n> ")
		if !scanner.Scan() {
			return currentPath, nil
		}
		input := strings.TrimSpace(scanner.Text())

		if input == "" {
			return currentPath, nil
		}
		if input == ".." {
			if currentPath == "/" || currentPath == "" {
				continue
			}
			currentPath = parentPath(currentPath)
			continue
		}

		dirNum, err := strconv.Atoi(input)
		if err != nil {
			fmt.Println("enter 0 to select, number to enter directory, or .. to go up")
			continue
		}
		if dirNum == 0 {
			return currentPath, nil
		}

		all := append(dirs, files...)
		if dirNum < 1 || dirNum > len(all) {
			fmt.Printf("enter 1-%d\n", len(all))
			continue
		}

		selected := all[dirNum-1]
		if !selected.IsDir {
			fmt.Printf("%s is not a directory\n", selected.Name)
			continue
		}

		currentPath = joinPath(currentPath, selected.Name)
	}
}

func parentPath(p string) string {
	if p == "/" || p == "" {
		return "/"
	}
	p = strings.TrimSuffix(p, "/")
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return "/"
	}
	return p[:idx]
}

func joinPath(base, name string) string {
	if base == "/" || base == "" {
		return "/" + name
	}
	return base + "/" + name
}

func init() {
	rootCmd.AddCommand(setupCmd)
}
