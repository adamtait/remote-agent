package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
)

func init() {
	pluginInstallCmd.Flags().BoolVar(&pluginInstallLocal, "local", false, "Install into .ra/plugins/ instead of ~/.config/ra/plugins/")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallForce, "force", false, "Overwrite existing plugin of the same name")
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	rootCmd.AddCommand(pluginCmd)
}

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage ra plugins",
}

var pluginInstallLocal bool
var pluginInstallForce bool

var pluginInstallCmd = &cobra.Command{
	Use:               "install <git-url>",
	Short:             "Install a plugin from a git repository",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		gitURL := args[0]

		tmp, err := os.MkdirTemp("", "ra-plugin-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)

		clone := exec.Command("git", "clone", "--depth=1", gitURL, tmp)
		clone.Stdout = os.Stdout
		clone.Stderr = os.Stderr
		if err := clone.Run(); err != nil {
			return fmt.Errorf("git clone: %w", err)
		}

		p, err := plugins.LoadPlugin(os.DirFS(tmp))
		if err != nil {
			return fmt.Errorf("reading plugin.yaml from cloned repo: %w", err)
		}

		var destBase string
		if pluginInstallLocal {
			wd, _ := os.Getwd()
			destBase = filepath.Join(wd, ".ra", "plugins")
		} else {
			home, _ := os.UserHomeDir()
			destBase = filepath.Join(home, ".config", "ra", "plugins")
		}
		dest := filepath.Join(destBase, p.Name())

		if _, err := os.Stat(dest); err == nil && !pluginInstallForce {
			return fmt.Errorf("plugin %q already exists at %s; use --force to overwrite", p.Name(), dest)
		}
		if err := os.RemoveAll(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(destBase, 0755); err != nil {
			return err
		}
		if err := copyDir(tmp, dest); err != nil {
			return fmt.Errorf("installing plugin: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q to %s\n", p.Name(), dest)
		return nil
	},
}

var pluginListCmd = &cobra.Command{
	Use:               "list",
	Short:             "List installed plugins",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		home, _ := os.UserHomeDir()
		wd, _ := os.Getwd()
		userDir := filepath.Join(home, ".config", "ra", "plugins")
		projDir := filepath.Join(wd, ".ra", "plugins")

		loaded, err := plugins.LoadAll(userDir, projDir)
		if err != nil {
			return err
		}
		if len(loaded) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(no plugins installed)")
			return nil
		}
		for _, p := range loaded {
			fmt.Fprintf(cmd.OutOrStdout(), "%-20s %s\n", p.Name(), p.Schema().Description)
		}
		return nil
	},
}

var pluginRemoveCmd = &cobra.Command{
	Use:               "remove <name>",
	Short:             "Remove an installed plugin",
	Args:              cobra.ExactArgs(1),
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		home, _ := os.UserHomeDir()
		dest := filepath.Join(home, ".config", "ra", "plugins", name)
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("plugin %q not found at %s", name, dest)
		}
		if err := os.RemoveAll(dest); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Removed plugin %q\n", name)
		return nil
	},
}

// copyDir recursively copies src directory to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, src)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
