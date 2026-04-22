package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [name]",
	Short: "Open an SSH session to a workstation (starts it if stopped)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		return runConnect(cfg, client, name, cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, errOut io.Writer) error {
	resolved, err := resolveWorkstationName(cfg, client, name)
	if err != nil {
		return err
	}
	info, err := client.Describe(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}
	if info.State != "STATE_RUNNING" && info.State != "STATE_STARTING" {
		fmt.Fprintf(errOut, "Workstation %q is %s; starting...\n", resolved, strings.TrimPrefix(info.State, "STATE_"))
		if err := client.Start(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved); err != nil {
			return fmt.Errorf("starting workstation: %w", err)
		}
	}
	return client.SSH(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
