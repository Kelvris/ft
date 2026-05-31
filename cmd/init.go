package cmd

import (
	"fmt"

	"ft/config"
	"ft/index"
	"ft/util"

	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init [url]",
	Short: "Initialize a new ft project",
	Long: `Creates .ft/ with config and index files.

If a URL is provided (e.g. ftp://user:pass@host:21/path),
it is added as the default remote "origin".

Otherwise use 'ft setup' for an interactive wizard.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := util.EnsureDir(util.FtDir); err != nil {
			return fmt.Errorf("creating .ft directory: %w", err)
		}

		cfg := &config.Config{
			Remotes: make(map[string]*config.Remote),
		}

		if len(args) > 0 {
			remote, err := config.ParseURL(args[0])
			if err != nil {
				return err
			}
			remote.Name = "origin"
			cfg.Remotes["origin"] = remote
			fmt.Printf("added remote: origin -> %s://%s:%d%s\n",
				remote.Protocol, remote.Host, remote.Port, remote.RemotePath)
		}

		if err := cfg.Save(); err != nil {
			return fmt.Errorf("saving config: %w", err)
		}

		idx := index.New()
		if err := idx.Save(); err != nil {
			return fmt.Errorf("saving index: %w", err)
		}

		fmt.Println("initialized empty ft project in .ft/")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
