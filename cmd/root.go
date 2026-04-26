package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/plugins"
	claudeplugin "github.com/adamtait/remote-agent/internal/plugins/claude"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	cfg    *config.Config
	client *gcloud.Client
	runner gcloud.Runner
	reg    *plugins.Registry
)

func init() {
	reg = plugins.NewRegistry(claudeplugin.New())
}

var rootCmd = &cobra.Command{
	Use:           "ra",
	Short:         "Manage Google Cloud Workstations with Claude Code",
	SilenceErrors: true, // cmd.Execute/ExecuteWithIO print errors explicitly
	SilenceUsage:  true, // don't dump --help on runtime errors
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
		userCfgPath, err := config.UserConfigPath()
		if err != nil {
			return fmt.Errorf("determining user config path: %w", err)
		}
		userCfg, err := config.LoadUserConfig(userCfgPath)
		if err != nil {
			return fmt.Errorf("loading user config: %w", err)
		}
		cfg, err = config.Merge(userCfg, projectCfg)
		if err != nil {
			return err
		}
		cfg.ProjectConfigPath = projectCfgPath
		if err := config.Validate(cfg, reg.ValidateAll(cfg)); err != nil {
			return err
		}
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

// ExecuteWithIO runs the root command with injectable IO and argv, returning
// an exit code. Used by tests; production still calls Execute().
//
// Flag values are reset to their declared defaults across the entire command
// tree before Execute, so repeated invocations (as in tests) can't leak flag
// state from a previous run.
func ExecuteWithIO(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	resetFlagsRecursive(rootCmd)
	rootCmd.SetIn(stdin)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func resetFlagsRecursive(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, child := range c.Commands() {
		resetFlagsRecursive(child)
	}
}
