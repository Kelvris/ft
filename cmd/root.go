package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var Version = "1.11.0"

var rootCmd = &cobra.Command{
	Use:   "ft",
	Short: "ft - Git-like FTP/SFTP sync tool",
	Long: `ft syncs local files with remote servers via FTP or SFTP.

Simple, fast, and secure file syncing with:
  - Concurrent uploads with transport-per-worker
  - Password vault (rotating secret directory)
  - Version snapshots for easy rollback
  - Interactive setup wizard
  - Dry-run, quiet mode, selective sync`,
	Run: func(cmd *cobra.Command, args []string) {
		cmd.Help()
	},
	// Errors are printed exactly once, by Execute — cobra's own print would
	// duplicate them and dump the usage text on runtime failures.
	SilenceUsage:  true,
	SilenceErrors: true,
}

// exitError carries a git-like exit code: 1 = conflict, 2 = transfer failure.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }
func (e *exitError) ExitCode() int { return e.code }

func exitErr(code int, err error) error {
	return &exitError{code: code, err: err}
}

// flagUsageError restores the usage text for bad flags, which SilenceUsage
// would otherwise swallow (cobra checks both the root and subcommand flags,
// so a root-level silence disables usage everywhere).
type flagUsageError struct {
	err   error
	usage string
}

func (e *flagUsageError) Error() string { return e.err.Error() }
func (e *flagUsageError) Unwrap() error { return e.err }

func Execute() {
	rootCmd.Version = Version
	rootCmd.SetVersionTemplate("ft v{{.Version}}\n")
	rootCmd.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return &flagUsageError{err: err, usage: c.UsageString()}
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)

		var usage *flagUsageError
		if errors.As(err, &usage) && usage.usage != "" {
			fmt.Fprintln(os.Stderr, usage.usage)
		} else if strings.HasPrefix(err.Error(), "unknown command") {
			fmt.Fprintf(os.Stderr, "Run '%v --help' for usage.\n", rootCmd.CommandPath())
		}

		code := 1
		var exit *exitError
		if errors.As(err, &exit) {
			code = exit.code
		}
		os.Exit(code)
	}
}
