package cmd

import (
	"github.com/spf13/cobra"
)

var logsTail int

var logsCmd = &cobra.Command{
	Use:   "logs [name]",
	Short: "Stream or snapshot container logs for a workstation",
	Long: `Stream container logs from Cloud Logging (default: --follow).
Use --tail=N to print the N most recent entries and exit.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		resolved, err := resolveWorkstationName(cfg, client, name)
		if err != nil {
			return err
		}
		follow := !cmd.Flags().Changed("tail")
		return client.Logs(cfg.ProjectID, resolved, follow, logsTail)
	},
}

func init() {
	logsCmd.Flags().IntVar(&logsTail, "tail", 0, "Print last N log entries and exit (disables streaming)")
	rootCmd.AddCommand(logsCmd)
}
