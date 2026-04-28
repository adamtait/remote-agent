package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	cfg        *config.Config
	client     *gcloud.Client
	runner     gcloud.Runner
	reg        *plugins.Registry
	logCleanup = func() {}
)

func init() {
	home, err := os.UserHomeDir()
	if err != nil {
		reg = plugins.NewRegistry(nil)
		return
	}
	userPluginsDir := filepath.Join(home, ".config", "ra", "plugins")
	loaded, err := plugins.LoadAll(userPluginsDir, "")
	if err != nil {
		reg = plugins.NewRegistry(nil)
		return
	}
	reg = plugins.NewRegistry(loaded)

	rootCmd.PersistentFlags().String("log-level", "info", "Log verbosity: debug, info, warn, error")
	rootCmd.PersistentFlags().String("log-file", "", "Log file path (default: auto in ~/.local/share/ra/logs/)")
	rootCmd.PersistentFlags().String("log-format", "json", "Log file format: json or text")
	rootCmd.PersistentFlags().Bool("no-log-file", false, "Disable file logging")
}

var rootCmd = &cobra.Command{
	Use:           "ra",
	Short:         "Provision and manage plugin-extensible Google Cloud Workstations",
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
		projectPluginsDir := filepath.Join(filepath.Dir(projectCfgPath), "plugins")
		loaded, err := plugins.LoadAll(
			filepath.Join(home, ".config", "ra", "plugins"),
			projectPluginsDir,
		)
		if err != nil {
			return fmt.Errorf("loading plugins: %w", err)
		}
		reg = plugins.NewRegistry(loaded)
		if err := config.Validate(cfg, reg.ValidateAll(cfg)); err != nil {
			return err
		}
		runner = gcloud.NewLoggingRunner(logging.L())
		client = gcloud.NewClient(runner)

		// Parse log flags
		logLevel, _ := cmd.Root().PersistentFlags().GetString("log-level")
		logFile, _ := cmd.Root().PersistentFlags().GetString("log-file")
		logFmt, _ := cmd.Root().PersistentFlags().GetString("log-format")
		noLogFile, _ := cmd.Root().PersistentFlags().GetBool("no-log-file")

		if os.Getenv("RA_NO_LOG_FILE") != "" {
			noLogFile = true
		}

		lvl := parseLogLevel(logLevel)

		cleanup, err := logging.Init(logging.Config{
			Level:   lvl,
			File:    logFile,
			Format:  logFmt,
			NoFile:  noLogFile,
			Command: cmd.Root().Name() + " " + cmd.Name(),
			Project: cfg.Gcloud.Project,
		})
		if err != nil {
			// Non-fatal: continue without file logging
			fmt.Fprintf(os.Stderr, "WARN: could not initialize logging: %v\n", err)
			logCleanup = func() {}
		} else {
			logCleanup = cleanup
		}

		return nil
	},
}

func parseLogLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func Execute() {
	defer logCleanup()
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
	defer logCleanup()
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
