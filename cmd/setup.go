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

		var (
			protocol   string
			port       int
			username   string
			password   string
			remotePath string
		)

		host := promptInput("Host", "")

		if strings.Contains(host, "://") {
			parsed, err := config.ParseURL(host)
			if err != nil {
				return fmt.Errorf("invalid URL: %w", err)
			}
			protocol = parsed.Protocol
			host = parsed.Host
			port = parsed.Port
			if port == 0 {
				port, _ = strconv.Atoi(defaultPortForProtocol(protocol))
			}
			username = parsed.Username
			password = parsed.Password
			remotePath = parsed.RemotePath
			fmt.Printf("Parsed: %s://%s@%s:%d%s\n", protocol, username, host, port, remotePath)

			// Prompt for any fields the URL didn't include
			if username == "" {
				username = promptInput("Username", "")
			}
			if password == "" {
				fmt.Printf("Password: ")
				passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					return fmt.Errorf("reading password: %w", err)
				}
				password = string(passwordBytes)
			}
		} else {
			protocol = promptChoice("Protocol", []string{"sftp", "ftp"}, "sftp")
			defaultPort := map[string]string{"sftp": "22", "ftp": "21"}[protocol]
			portStr := promptInput("Port", defaultPort)
			port, _ = strconv.Atoi(portStr)
			if port == 0 {
				port, _ = strconv.Atoi(defaultPortForProtocol(protocol))
			}
			username = promptInput("Username", "")

			fmt.Printf("Password: ")
			passwordBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
			fmt.Println()
			if err != nil {
				return fmt.Errorf("reading password: %w", err)
			}
			password = string(passwordBytes)
			remotePath = "/"
		}

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
			RemotePath: remotePath,
		}
		remote.Name = "origin"

		t, err := transport.NewTransport(remote)
		if err != nil {
			return fmt.Errorf("creating transport: %w", err)
		}
		if err := t.Connect(); err != nil {
			t.Close()
			return fmt.Errorf("connection failed: %w", err)
		}
		defer t.Close()
		fmt.Println("Connected!")
		fmt.Println()

		startPath := "/"
		if remotePath != "" && remotePath != "/" {
			startPath = remotePath
		}
		chosenPath, err := browseInteractive(t, startPath)
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

func defaultPortForProtocol(protocol string) string {
	switch protocol {
	case "ftp":
		return "21"
	default:
		return "22"
	}
}

func browseInteractive(t transport.Transport, currentPath string) (string, error) {
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return "", fmt.Errorf("terminal raw mode: %w", err)
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	cursor := 0

	for {
		entries, err := t.ListDir(currentPath)
		if err != nil {
			return "", fmt.Errorf("listing %s: %w", currentPath, err)
		}

		// Only show directories — files are irrelevant for path selection
		dirs := []transport.DirEntry{}
		for _, e := range entries {
			if strings.HasPrefix(e.Name, ".") {
				continue
			}
			if e.IsDir {
				dirs = append(dirs, e)
			}
		}

		maxIdx := len(dirs)
		if cursor < 0 {
			cursor = 0
		} else if cursor > maxIdx {
			cursor = maxIdx
		}

		renderBrowser(currentPath, dirs, cursor)

		key := readKey()

		switch {
		case key == "up":
			if cursor > 0 {
				cursor--
			}
		case key == "down":
			if cursor < maxIdx {
				cursor++
			}
		case key == "enter":
			if cursor == 0 {
				return currentPath, nil
			}
			selected := dirs[cursor-1]
			currentPath = joinPath(currentPath, selected.Name)
			cursor = 0
		case key == "right":
			if cursor > 0 {
				selected := dirs[cursor-1]
				currentPath = joinPath(currentPath, selected.Name)
				cursor = 0
			}
		case key == "left" || key == "backspace" || key == "esc":
			if currentPath == "/" || currentPath == "" {
				return currentPath, nil
			}
			currentPath = parentPath(currentPath)
			cursor = 0
		case key == "quit":
			return "", nil
		default:
			// Try digit jump — type a number to jump cursor to that entry
			if n, err := strconv.Atoi(key); err == nil && n >= 0 && n <= maxIdx {
				cursor = n
			}
		}
	}
}

func readKey() string {
	buf := make([]byte, 8)
	n, err := os.Stdin.Read(buf)
	if err != nil || n == 0 {
		return "quit"
	}

	switch {
	case buf[0] == 13 || buf[0] == 10:
		return "enter"

	case buf[0] == 27:
		if n >= 3 && buf[1] == 91 {
			switch buf[2] {
			case 65:
				return "up"
			case 66:
				return "down"
			case 67:
				return "right"
			case 68:
				return "left"
			}
		}
		return "esc"

	case buf[0] == 127 || buf[0] == 8:
		return "backspace"

	case buf[0] == 'q' || buf[0] == 'Q':
		return "quit"

	case buf[0] >= '0' && buf[0] <= '9':
		return string(buf[0])
	}
	return ""
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r != 127 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func renderBrowser(currentPath string, dirs []transport.DirEntry, cursor int) {
	fmt.Print("\x1b[H\x1b[2J")

	display := currentPath
	if display == "" {
		display = "/"
	}
	fmt.Printf("Contents of %s:\r\n", display)

	// Option 0: use current directory
	if cursor == 0 {
		fmt.Printf("  \x1b[7m[0] Use this directory\x1b[0m\r\n")
	} else {
		fmt.Printf("  [0] Use this directory\r\n")
	}

	if currentPath != "/" && currentPath != "" {
		fmt.Printf("  [..] Go up (Left/Backspace)\r\n")
	}

	// List directories
	for i, d := range dirs {
		idx := i + 1
		if cursor == idx {
			fmt.Printf("  \x1b[7m[%d] /%s/\x1b[0m\r\n", idx, sanitizeName(d.Name))
		} else {
			fmt.Printf("  [%d] /%s/\r\n", idx, sanitizeName(d.Name))
		}
	}

	fmt.Print("\r\nArrow keys/type number to navigate, Enter to select, Left/Backspace to go up, q to cancel")
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
