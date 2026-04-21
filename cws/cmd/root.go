package cmd

import (
	"fmt"
	"os"

	"github.com/adamtait/remote-agent/cws/internal/config"
	"github.com/adamtait/remote-agent/cws/internal/gcloud"
	"github.com/spf13/cobra"
)

var (
	cfg    *config.Config
	client *gcloud.Client
	runner gcloud.Runner
)

var rootCmd = &cobra.Command{
	Use:   "cws",
	Short: "Manage Google Cloud Workstations with Claude Code",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		projectCfgPath, err := config.FindProjectConfig(wd, home)
		if err != nil {
			return err
		}
		projectCfg, err := config.LoadProjectConfig(projectCfgPath)
		if err != nil {
			return fmt.Errorf("loading %s: %w", projectCfgPath, err)
		}
		globalCfgPath, err := config.GlobalConfigPath()
		if err != nil {
			return fmt.Errorf("determining global config path: %w", err)
		}
		globalCfg, err := config.LoadGlobalConfig(globalCfgPath)
		if err != nil {
			return fmt.Errorf("loading global config: %w", err)
		}
		cfg, err = config.Merge(projectCfg, globalCfg)
		if err != nil {
			return err
		}
		cfg.ProjectConfigPath = projectCfgPath
		runner = gcloud.RealRunner{}
		client = gcloud.NewClient(runner)
		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
