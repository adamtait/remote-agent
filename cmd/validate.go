package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate ra config files (user, project, and merged)",
	Long: `Loads the user config, the project config (if any), and their merge,
then validates each independently and prints a per-layer report. Project
config gets structural checks only (secret/env shape and collisions); user
and merged get the full check including required fields and plugin validation.

Bypasses the usual preflight so it can run when the merged config is broken —
that is precisely when you need to inspect the layers.`,
	// Override root's PersistentPreRunE — validate must run even when the
	// merged config fails the standard preflight check.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		show, _ := cmd.Flags().GetBool("show")
		out := cmd.OutOrStdout()

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}

		userCfgPath, err := config.UserConfigPath()
		if err != nil {
			return fmt.Errorf("determining user config path: %w", err)
		}
		projectCfgPath, err := config.FindProjectConfig(wd, home)
		if err != nil {
			return fmt.Errorf("locating project config: %w", err)
		}

		anyFailed := false

		// User layer
		userCfg, userLoadErr := config.LoadUserConfig(userCfgPath)
		if userLoadErr != nil {
			fmt.Fprintf(out, "user config:    %s — FAIL\n  - %v\n", userCfgPath, userLoadErr)
			anyFailed = true
		} else {
			userPluginErrs := pluginValidate(home, "", userCfg, nil)
			if err := config.ValidateUser(userCfg, userPluginErrs); err != nil {
				fmt.Fprintf(out, "user config:    %s — FAIL\n%s\n", userCfgPath, formatLayerErr(err))
				anyFailed = true
			} else {
				fmt.Fprintf(out, "user config:    %s — OK\n", userCfgPath)
			}
		}

		// Project layer
		var projectCfg *config.ProjectConfig
		var projectLoadErr error
		if projectCfgPath == "" {
			fmt.Fprintf(out, "project config: (none — no .ra/config.yaml found)\n")
		} else {
			projectCfg, projectLoadErr = config.LoadProjectConfig(projectCfgPath)
			if projectLoadErr != nil {
				fmt.Fprintf(out, "project config: %s — FAIL\n  - %v\n", projectCfgPath, projectLoadErr)
				anyFailed = true
			} else if err := config.ValidateProject(projectCfg); err != nil {
				fmt.Fprintf(out, "project config: %s — FAIL\n%s\n", projectCfgPath, formatLayerErr(err))
				anyFailed = true
			} else {
				fmt.Fprintf(out, "project config: %s — OK\n", projectCfgPath)
			}
		}

		// Merged layer — requires both layers to have loaded.
		var mergedCfg *config.Config
		canMerge := userLoadErr == nil && projectLoadErr == nil
		if canMerge {
			if projectCfg == nil {
				projectCfg = &config.ProjectConfig{}
			}
			merged, err := config.Merge(userCfg, projectCfg)
			if err != nil {
				fmt.Fprintf(out, "merged config:  FAIL\n  - merge: %v\n", err)
				anyFailed = true
			} else {
				merged.ProjectConfigPath = projectCfgPath
				mergedCfg = merged
				mergedPluginErrs := pluginValidate(home, projectCfgPath, nil, merged)
				if err := config.Validate(merged, mergedPluginErrs); err != nil {
					fmt.Fprintf(out, "merged config:  FAIL\n%s\n", formatLayerErr(err))
					anyFailed = true
				} else {
					fmt.Fprintf(out, "merged config:  OK\n")
				}
			}
		} else {
			fmt.Fprintf(out, "merged config:  SKIPPED (a layer failed to load)\n")
		}

		if show && mergedCfg != nil {
			fmt.Fprintln(out, "\n--- merged config ---")
			if err := writeYAML(out, mergedCfg); err != nil {
				return fmt.Errorf("rendering merged config: %w", err)
			}
		}

		if anyFailed {
			return errSilent
		}
		return nil
	},
}

// pluginValidate loads the plugin registry from the appropriate dirs and
// returns the combined []string error list. When mergedCfg is non-nil the
// project plugins dir is included; otherwise only user-level plugins are
// loaded (used when validating the user layer in isolation).
func pluginValidate(home, projectCfgPath string, userCfg *config.UserConfig, mergedCfg *config.Config) []string {
	userPluginsDir := filepath.Join(home, ".config", "ra", "plugins")
	projectPluginsDir := ""
	if mergedCfg != nil && projectCfgPath != "" {
		projectPluginsDir = filepath.Join(filepath.Dir(projectCfgPath), "plugins")
	}
	loaded, err := plugins.LoadAll(userPluginsDir, projectPluginsDir)
	if err != nil {
		return []string{fmt.Sprintf("loading plugins: %v", err)}
	}
	r := plugins.NewRegistry(loaded)
	if mergedCfg != nil {
		return r.ValidateAll(mergedCfg)
	}
	// Validate user layer against user-level plugins only.
	probe := &config.Config{
		Git:     userCfg.Git,
		Gcloud:  userCfg.Gcloud,
		Plugins: userCfg.Plugins,
		Secrets: userCfg.Secrets,
		Env:     userCfg.Env,
	}
	return r.ValidateAll(probe)
}

// formatLayerErr trims the leading "X validation failed:" line from a
// validator error and indents the remaining bullet list so it nests cleanly
// under a "<layer>: <path> — FAIL" header without echoing the same phrase.
func formatLayerErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "\n"); i != -1 {
		s = s[i+1:]
	}
	return "  " + strings.ReplaceAll(s, "\n", "\n  ")
}

func writeYAML(w io.Writer, v interface{}) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return enc.Close()
}

func init() {
	validateCmd.Flags().Bool("show", false, "after the report, print the merged config as YAML")
	rootCmd.AddCommand(validateCmd)
}
