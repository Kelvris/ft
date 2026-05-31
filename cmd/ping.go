package cmd

import (
	"fmt"
	"time"

	"github.com/Kelvris/ft/config"
	"github.com/Kelvris/ft/transport"

	"github.com/spf13/cobra"
)

var pingCmd = &cobra.Command{
	Use:   "ping [remote]",
	Short: "Test connection to remote server",
	Long:  `Connects to the remote and reports latency.`,
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		remoteName := "origin"
		if len(args) > 0 {
			remoteName = args[0]
		}

		cfg, err := config.LoadConfig()
		if err != nil {
			return fmt.Errorf("loading config: %w", err)
		}
		remote, exists := cfg.Remotes[remoteName]
		if !exists {
			return fmt.Errorf("remote %q not found", remoteName)
		}

		if err := resolvePassword(remote, remoteName); err != nil {
			return err
		}

		t, err := transport.NewTransport(remote)
		if err != nil {
			return err
		}

		start := time.Now()
		if err := t.Connect(); err != nil {
			return fmt.Errorf("connection failed: %w", err)
		}
		elapsed := time.Since(start)
		t.Close()

		fmt.Printf("connected to %s://%s:%d%s in %s\n",
			remote.Protocol, remote.Host, remote.Port, remote.RemotePath, elapsed)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(pingCmd)
}
