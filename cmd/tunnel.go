package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
)

var tunnelCmd = &cobra.Command{
	Use:   "tunnel <target> [workstation]",
	Short: "Open a local TCP tunnel to a workstation port",
	Long: `Open a foreground TCP tunnel from the local machine to a port inside the workstation.

The target argument can be:
  - A plugin name (e.g. "marimo")          — remote port comes from the plugin's
                                             port: field in plugin.yaml; local port
                                             defaults to the same value (or --local-port).
  - A single port (e.g. "8080")            — both local and remote port equal.
  - A "local:remote" pair (e.g. "9000:80") — explicit local/remote ports.

If [workstation] is omitted, the active workstation from config is used (same
selection rules as ` + "`ra connect`" + `). Auto-starts the workstation if stopped.
Blocks until Ctrl-C.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: func(cmd *cobra.Command, args []string) error {
		localOverride, _ := cmd.Flags().GetInt("local-port")
		localPort, remotePort, err := parseTunnelTarget(args[0], localOverride, reg)
		if err != nil {
			return err
		}
		var wsName string
		if len(args) == 2 {
			wsName = args[1]
		}
		return runTunnel(cfg, client, wsName, readLocationOverrides(cmd), localPort, remotePort, cmd.ErrOrStderr())
	},
}

// parseTunnelTarget converts the first CLI argument into (localPort, remotePort).
//
//	"L:R"   → (L, R) — localOverride is rejected (already explicit)
//	"P"     → (P, P) — localOverride wins if set
//	"name"  → (reg.PortByName(name), same) — localOverride wins if set
func parseTunnelTarget(arg string, localOverride int, reg *plugins.Registry) (int, int, error) {
	if strings.Contains(arg, ":") {
		parts := strings.SplitN(arg, ":", 2)
		l, errL := strconv.Atoi(parts[0])
		r, errR := strconv.Atoi(parts[1])
		if errL != nil || errR != nil {
			return 0, 0, fmt.Errorf("invalid local:remote port pair %q: both must be integers", arg)
		}
		if err := validatePort(l, "local"); err != nil {
			return 0, 0, err
		}
		if err := validatePort(r, "remote"); err != nil {
			return 0, 0, err
		}
		if localOverride != 0 {
			return 0, 0, fmt.Errorf("--local-port conflicts with explicit local:remote pair %q", arg)
		}
		return l, r, nil
	}

	if p, err := strconv.Atoi(arg); err == nil {
		if err := validatePort(p, "port"); err != nil {
			return 0, 0, err
		}
		local := p
		if localOverride != 0 {
			if err := validatePort(localOverride, "--local-port"); err != nil {
				return 0, 0, err
			}
			local = localOverride
		}
		return local, p, nil
	}

	remote, ok := reg.PortByName(arg)
	if !ok {
		avail := reg.TunnelablePlugins()
		if len(avail) == 0 {
			return 0, 0, fmt.Errorf("no installed plugin named %q declares a tunnel port (no plugins declare a port: field)", arg)
		}
		return 0, 0, fmt.Errorf("no installed plugin named %q declares a tunnel port; available: %s", arg, strings.Join(avail, ", "))
	}
	local := remote
	if localOverride != 0 {
		if err := validatePort(localOverride, "--local-port"); err != nil {
			return 0, 0, err
		}
		local = localOverride
	}
	return local, remote, nil
}

func validatePort(p int, label string) error {
	if p < 1 || p > 65535 {
		return fmt.Errorf("%s %d is out of range (1-65535)", label, p)
	}
	return nil
}

func runTunnel(cfg *config.Config, client *gcloud.Client, name string, override wsOverride, localPort, remotePort int, errOut io.Writer) error {
	ref, err := resolveWorkstationRef(cfg, client, name, override)
	if err != nil {
		return err
	}
	if err := ensureRunning(client, ref, errOut); err != nil {
		return err
	}
	fmt.Fprintf(errOut, "Tunneling localhost:%d -> %s:%d (Ctrl-C to stop)\n", localPort, ref.Name, remotePort)
	return client.StartTCPTunnel(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name, localPort, remotePort)
}

func init() {
	tunnelCmd.Flags().Int("local-port", 0, "Override local port (named or single-port targets only)")
	addLocationOverrideFlags(tunnelCmd)
	rootCmd.AddCommand(tunnelCmd)
}
