package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
)

// reservedPluginSubcommands are the built-in `ra plugin` subcommands a plugin
// name must not shadow when it contributes commands.
var reservedPluginSubcommands = map[string]bool{"install": true, "list": true, "remove": true}

func init() {
	registerPluginCommands(pluginCmd, loadCommandPlugins())
}

// loadCommandPlugins loads the installed plugins (user + project level) for the
// purpose of registering their declared commands. It runs at process start
// (init), before cfg is available, so it derives the project plugins dir the
// same way PersistentPreRunE does — by walking up from cwd for .ra/config.yaml.
// Best-effort: any error yields whatever loaded (possibly nil).
func loadCommandPlugins() []*plugins.FilePlugin {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	userDir := filepath.Join(home, ".config", "ra", "plugins")
	projDir := ""
	if wd, err := os.Getwd(); err == nil {
		if p, err := config.FindProjectConfig(wd, home); err == nil && p != "" {
			projDir = filepath.Join(filepath.Dir(p), "plugins")
		}
	}
	ps, _ := plugins.LoadAll(userDir, projDir)
	return ps
}

// registerPluginCommands adds `ra plugin <plugin-name> <command-name>` for every
// command an installed plugin declares in its plugin.yaml. It must run before
// rootCmd.Execute() (cobra selects the target command from the already-built
// tree), which the package init above guarantees. A plugin whose name collides
// with a built-in `ra plugin` subcommand is skipped with a warning; core stays
// plugin-agnostic (no plugin name is hard-coded).
func registerPluginCommands(parent *cobra.Command, ps []*plugins.FilePlugin) {
	if parent == nil {
		return
	}
	for _, p := range ps {
		specs := p.Schema().Commands
		if len(specs) == 0 {
			continue
		}
		if reservedPluginSubcommands[p.Name()] {
			fmt.Fprintf(os.Stderr, "ra: plugin %q shadows a built-in `ra plugin` subcommand; its commands are not registered\n", p.Name())
			continue
		}
		group := &cobra.Command{
			Use:   p.Name(),
			Short: fmt.Sprintf("Commands contributed by the %q plugin", p.Name()),
		}
		for _, spec := range specs {
			group.AddCommand(newPluginCommand(p, spec))
		}
		parent.AddCommand(group)
	}
}

// newPluginCommand builds the leaf cobra command for one plugin CommandSpec.
// Its RunE reads the cfg/client globals lazily (they are populated by the root
// PersistentPreRunE, which cobra runs before any RunE).
func newPluginCommand(p *plugins.FilePlugin, spec plugins.CommandSpec) *cobra.Command {
	c := &cobra.Command{
		Use:   spec.Name + " [name]",
		Short: spec.Description,
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPluginCommand(cmd, p, spec, args)
		},
	}
	if spec.NeedsWorkstation {
		addLocationOverrideFlags(c)
		addSelectionFlags(c)
	}
	return c
}

// runPluginCommand execs a plugin command's local script with the RA_* env
// contract. When the command needs a workstation, ra resolves it (optional
// [name] + --project/--cluster/--config, plus --workstation/--select) and ensures
// it is running first, exporting its coordinates. Because it uses
// readSelectionOverrides, the workstation picker is available (like `ra connect`).
// The `ra` binary's directory is prepended to PATH so the script can call back
// into `ra` (e.g. `ra tunnel`).
func runPluginCommand(cmd *cobra.Command, p *plugins.FilePlugin, spec plugins.CommandSpec, args []string) error {
	if p.Dir() == "" {
		return fmt.Errorf("plugin %q: cannot locate its directory to run command %q", p.Name(), spec.Name)
	}
	script := filepath.Join(p.Dir(), spec.Run)
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("plugin %q command %q: script %q not found: %w", p.Name(), spec.Name, spec.Run, err)
	}

	env := os.Environ()
	if exe, err := os.Executable(); err == nil {
		env = append(env, "PATH="+filepath.Dir(exe)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}

	if spec.NeedsWorkstation {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		ref, err := resolveWorkstationRef(cfg, client, name, readSelectionOverrides(cmd))
		if err != nil {
			return err
		}
		if _, err := ensureRunning(client, ref, cmd.ErrOrStderr()); err != nil {
			return err
		}
		env = append(env,
			"RA_WORKSTATION="+ref.Name,
			"RA_PROJECT="+ref.Project,
			"RA_REGION="+ref.Region,
			"RA_CLUSTER="+ref.Cluster,
			"RA_CONFIG="+ref.Config,
			"RA_WORKSTATION_USER=user",
			"RA_LOGIN_COMMAND="+cfg.Connect.LoginCommand,
		)
	}

	sub := exec.Command(script)
	sub.Env = env
	sub.Stdin = cmd.InOrStdin()
	sub.Stdout = cmd.OutOrStdout()
	sub.Stderr = cmd.ErrOrStderr()
	if err := sub.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// The script ran and exited non-zero — it already reported its own
			// failure to the terminal; exit non-zero without ra printing over it.
			return errSilent
		}
		// The script could not be started at all (not executable, bad
		// interpreter, …) — surface a real error, since nothing was printed.
		return fmt.Errorf("plugin %q command %q: %w", p.Name(), spec.Name, err)
	}
	return nil
}
