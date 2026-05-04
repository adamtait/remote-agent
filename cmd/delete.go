package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var deleteForce bool

var deleteCmd = &cobra.Command{
	Use:   "delete [name]",
	Short: "Delete a workstation",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		ref, err := resolveWorkstationRef(cfg, client, name, readLocationOverrides(cmd))
		if err != nil {
			return err
		}

		if !deleteForce {
			if !term.IsTerminal(int(os.Stdin.Fd())) {
				return fmt.Errorf("stdin is not a terminal; use --force to delete non-interactively")
			}
			fmt.Fprintf(os.Stderr, "Delete workstation %q (%s/%s/%s)? [y/N]: ", ref.Name, ref.Project, ref.Cluster, ref.Config)
			scanner := bufio.NewScanner(os.Stdin)
			if !scanner.Scan() {
				return fmt.Errorf("no input received")
			}
			if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
				fmt.Fprintln(os.Stderr, "Aborted.")
				return nil
			}
		}

		return client.Delete(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
	},
}

func init() {
	deleteCmd.Flags().BoolVar(&deleteForce, "force", false, "Skip confirmation prompt")
	addLocationOverrideFlags(deleteCmd)
	rootCmd.AddCommand(deleteCmd)
}
