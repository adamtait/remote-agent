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
		return runConnect(cfg, client, name, readLocationOverrides(cmd), cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, override wsOverride, errOut io.Writer) error {
	ref, err := resolveWorkstationRef(cfg, client, name, override)
	if err != nil {
		return err
	}
	info, err := client.Describe(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}
	if info.State != "STATE_RUNNING" && info.State != "STATE_STARTING" {
		fmt.Fprintf(errOut, "Workstation %q is %s; starting...\n", ref.Name, strings.TrimPrefix(info.State, "STATE_"))
		if err := client.Start(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name); err != nil {
			return fmt.Errorf("starting workstation: %w", err)
		}
	}
	return client.SSH(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
}

func init() {
	addLocationOverrideFlags(connectCmd)
	rootCmd.AddCommand(connectCmd)
}
