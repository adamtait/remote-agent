package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/gcs"
	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/adamtait/remote-agent/internal/secrets"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// totalTeardownSteps is the fixed number of teardown phases, shown as
// "[n/total]" in progress output. Conditional phases (secrets, buckets) still
// consume a number and print a skip note when disabled, mirroring the fixed
// step count of `ra create`.
const totalTeardownSteps = 8

// baseProjectRoles are the project-level IAM roles `ra create` always grants
// the workstation service account (create.go Step 8). Teardown removes the same
// set (plus any plugin auth-provider roles) so no stale bindings linger.
var baseProjectRoles = []string{
	"roles/logging.logWriter",
	"roles/monitoring.metricWriter",
}

// teardownOpts captures the teardown flags.
type teardownOpts struct {
	Force         bool
	DryRun        bool
	DeleteBuckets bool
	KeepSecrets   bool
}

var teardownFlags teardownOpts

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Delete all gcloud resources ra created (workstations, cluster, SA, repo, secrets)",
	Long: `Delete every GCP resource provisioned by 'ra create', in dependency-safe
order: workstation instances, the workstations config, the cluster (which accrues
the fixed control-plane fee), project IAM bindings, the service account, Secret
Manager secrets, and the Artifact Registry repository.

Project APIs are left enabled (they are project-wide and unsafe to disable).
Plugin GCS buckets are preserved unless --delete-buckets is passed, because their
contents (e.g. synced work artifacts) are not recoverable.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		override := readLocationOverrides(cmd)
		ws := cfg.Gcloud.Workstation
		ref := gcloud.WorkstationRef{
			Project: pickStr(override.Project, cfg.Gcloud.Project),
			Region:  ws.Region,
			Cluster: pickStr(override.Cluster, ws.ClusterName),
			Config:  pickStr(override.Config, ws.ConfigName),
		}
		return runTeardown(client, runner, cfg, reg, ref, teardownFlags, cmd.OutOrStdout())
	},
}

func init() {
	teardownCmd.Flags().BoolVar(&teardownFlags.Force, "force", false, "Skip confirmation prompt")
	teardownCmd.Flags().BoolVar(&teardownFlags.DryRun, "dry-run", false, "Print the deletion plan and exit without deleting anything")
	teardownCmd.Flags().BoolVar(&teardownFlags.DeleteBuckets, "delete-buckets", false, "Also delete plugin GCS buckets AND their contents (unrecoverable)")
	teardownCmd.Flags().BoolVar(&teardownFlags.KeepSecrets, "keep-secrets", false, "Preserve Secret Manager secrets instead of deleting them")
	addLocationOverrideFlags(teardownCmd)
	rootCmd.AddCommand(teardownCmd)
}

// runTeardown deletes the ra-provisioned resources identified by ref + cfg in
// reverse dependency order. Each resource is deleted only if it exists
// (check-then-delete), so re-runs converge cleanly; a delete that fails on an
// existing resource is collected and surfaced as an error at the end without
// stranding the remaining steps.
func runTeardown(client *gcloud.Client, r gcloud.Runner, cfg *config.Config, reg *plugins.Registry, ref gcloud.WorkstationRef, opts teardownOpts, out io.Writer) error {
	if ref.Project == "" {
		return fmt.Errorf("project not set (config gcloud.project or --project)")
	}

	ws := cfg.Gcloud.Workstation
	saEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", ws.ServiceAccountName, ref.Project)
	roles := appendUniqueRoles(append([]string{}, baseProjectRoles...), pluginAuthIAMRoles(reg.All(), cfg))

	secretNames := teardownSecretNames(cfg)
	buckets, err := pluginBuckets(reg, cfg)
	if err != nil {
		return fmt.Errorf("resolving plugin buckets: %w", err)
	}

	// Enumerate workstation instances up front (also used in the plan preview).
	var instances []string
	if ref.Cluster != "" && ref.Config != "" {
		listed, lerr := client.List(ref.Project, ref.Region, ref.Cluster, ref.Config)
		if lerr != nil {
			// A missing cluster/config yields an error here; treat as "nothing to
			// list" so teardown can still clean up the rest and is safe to re-run.
			logging.WarnPrintf(out, "could not list workstations (cluster/config may already be gone): %v", lerr)
		} else {
			for _, w := range listed {
				instances = append(instances, w.Name)
			}
		}
	}

	printTeardownPlan(out, ref, saEmail, ws.RepoName, roles, instances, secretNames, buckets, opts)

	if opts.DryRun {
		fmt.Fprintln(out, "\nDry run — no resources were deleted.")
		return nil
	}

	if !opts.Force {
		if !term.IsTerminal(int(os.Stdin.Fd())) {
			return fmt.Errorf("stdin is not a terminal; re-run with --force to tear down non-interactively")
		}
		fmt.Fprintf(os.Stderr, "\nDelete all the above resources in project %q? This cannot be undone. [y/N]: ", ref.Project)
		scanner := bufio.NewScanner(os.Stdin)
		if !scanner.Scan() {
			return fmt.Errorf("no input received")
		}
		if strings.ToLower(strings.TrimSpace(scanner.Text())) != "y" {
			fmt.Fprintln(os.Stderr, "Aborted.")
			return nil
		}
	}

	var failures []error
	// fail records a real deletion failure (resource existed but delete errored).
	fail := func(what string, err error) {
		logging.WarnPrintf(out, "failed to delete %s: %v", what, err)
		failures = append(failures, fmt.Errorf("%s: %w", what, err))
	}

	// Step 1 — workstation instances.
	done := logging.Step("teardown_workstations", 1, totalTeardownSteps,
		fmt.Sprintf("Deleting %d workstation instance(s)...", len(instances)), out)
	for _, name := range instances {
		if err := client.Delete(ref.Project, ref.Region, ref.Cluster, ref.Config, name); err != nil {
			fail(fmt.Sprintf("workstation %q", name), err)
		}
	}
	done(nil)

	// Step 2 — workstations config.
	done = logging.Step("teardown_config", 2, totalTeardownSteps, "Deleting workstations config...", out)
	if ref.Cluster != "" && ref.Config != "" &&
		r.Check("workstations", "configs", "describe", ref.Config, "--cluster", ref.Cluster, "--region", ref.Region, "--project", ref.Project) {
		if err := client.DeleteConfig(ref.Project, ref.Region, ref.Cluster, ref.Config); err != nil {
			fail(fmt.Sprintf("config %q", ref.Config), err)
		}
	}
	done(nil)

	// Step 3 — workstations cluster (the control-plane-fee resource).
	done = logging.Step("teardown_cluster", 3, totalTeardownSteps, "Deleting workstations cluster...", out)
	if ref.Cluster != "" &&
		r.Check("workstations", "clusters", "describe", ref.Cluster, "--region", ref.Region, "--project", ref.Project) {
		if err := client.DeleteCluster(ref.Project, ref.Region, ref.Cluster); err != nil {
			fail(fmt.Sprintf("cluster %q", ref.Cluster), err)
		}
	}
	done(nil)

	// Step 4 — project IAM bindings (best-effort; removing an absent binding is
	// harmless, so no existence check).
	done = logging.Step("teardown_project_iam", 4, totalTeardownSteps,
		fmt.Sprintf("Removing %d project IAM binding(s)...", len(roles)), out)
	for _, role := range roles {
		_ = r.RunSoft("projects", "remove-iam-policy-binding", ref.Project,
			"--member=serviceAccount:"+saEmail, "--role="+role, "--condition=None")
	}
	done(nil)

	// Step 5 — service account (deleting it invalidates its secret/bucket/AR
	// bindings, so those need no explicit removal).
	done = logging.Step("teardown_service_account", 5, totalTeardownSteps, "Deleting service account...", out)
	if r.Check("iam", "service-accounts", "describe", saEmail, "--project", ref.Project) {
		if err := r.Run("iam", "service-accounts", "delete", saEmail, "--project", ref.Project, "--quiet"); err != nil {
			fail(fmt.Sprintf("service account %q", saEmail), err)
		}
	}
	done(nil)

	// Step 6 — Secret Manager secrets.
	if opts.KeepSecrets {
		logging.Step("teardown_secrets", 6, totalTeardownSteps, "Deleting secrets... (skipped: --keep-secrets)", out)(nil)
	} else {
		done = logging.Step("teardown_secrets", 6, totalTeardownSteps,
			fmt.Sprintf("Deleting %d secret(s)...", len(secretNames)), out)
		for _, name := range secretNames {
			if secrets.Exists(r, ref.Project, name) {
				if err := secrets.Delete(r, ref.Project, name); err != nil {
					fail(fmt.Sprintf("secret %q", name), err)
				}
			}
		}
		done(nil)
	}

	// Step 7 — plugin GCS buckets (opt-in; unrecoverable contents).
	if !opts.DeleteBuckets {
		msg := "Deleting plugin buckets... (skipped: pass --delete-buckets to remove)"
		logging.Step("teardown_buckets", 7, totalTeardownSteps, msg, out)(nil)
	} else {
		done = logging.Step("teardown_buckets", 7, totalTeardownSteps,
			fmt.Sprintf("Deleting %d plugin bucket(s)...", len(buckets)), out)
		for _, b := range buckets {
			if r.Check("storage", "buckets", "describe", "gs://"+b.Name, "--project", ref.Project) {
				if err := gcs.DeleteBucket(r, ref.Project, b.Name); err != nil {
					fail(fmt.Sprintf("bucket %q", b.Name), err)
				}
			}
		}
		done(nil)
	}

	// Step 8 — Artifact Registry repository (removes images too).
	done = logging.Step("teardown_repo", 8, totalTeardownSteps, "Deleting Artifact Registry repository...", out)
	if ws.RepoName != "" &&
		r.Check("artifacts", "repositories", "describe", ws.RepoName, "--location", ref.Region, "--project", ref.Project) {
		if err := r.Run("artifacts", "repositories", "delete", ws.RepoName,
			"--location", ref.Region, "--project", ref.Project, "--quiet"); err != nil {
			fail(fmt.Sprintf("repository %q", ws.RepoName), err)
		}
	}
	done(nil)

	if len(failures) > 0 {
		fmt.Fprintf(out, "\nTeardown finished with %d error(s); re-run to retry the remaining resources.\n", len(failures))
		return errors.Join(failures...)
	}
	fmt.Fprintln(out, "\nTeardown complete. All ra-provisioned resources removed (APIs left enabled).")
	return nil
}

// teardownSecretNames returns the GCP secret names from cfg.Secrets, sorted by
// logical key for deterministic ordering/output.
func teardownSecretNames(cfg *config.Config) []string {
	keys := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		if n := cfg.Secrets[k].SecretName; n != "" {
			names = append(names, n)
		}
	}
	return names
}

// printTeardownPlan prints the human-readable list of what teardown will delete.
func printTeardownPlan(out io.Writer, ref gcloud.WorkstationRef, saEmail, repo string, roles, instances, secretNames []string, buckets []resolvedBucket, opts teardownOpts) {
	fmt.Fprintf(out, "Teardown plan for project %q (region %s):\n", ref.Project, ref.Region)
	if len(instances) > 0 {
		fmt.Fprintf(out, "  • %d workstation instance(s): %s\n", len(instances), strings.Join(instances, ", "))
	} else {
		fmt.Fprintf(out, "  • workstation instances: none found\n")
	}
	fmt.Fprintf(out, "  • workstations config:  %s\n", orNone(ref.Config))
	fmt.Fprintf(out, "  • workstations cluster: %s\n", orNone(ref.Cluster))
	fmt.Fprintf(out, "  • project IAM bindings: %s → %s\n", saEmail, strings.Join(roles, ", "))
	fmt.Fprintf(out, "  • service account:      %s\n", saEmail)
	if opts.KeepSecrets {
		fmt.Fprintf(out, "  • secrets:              KEPT (--keep-secrets)\n")
	} else if len(secretNames) > 0 {
		fmt.Fprintf(out, "  • %d secret(s):          %s\n", len(secretNames), strings.Join(secretNames, ", "))
	} else {
		fmt.Fprintf(out, "  • secrets:              none configured\n")
	}
	if !opts.DeleteBuckets {
		fmt.Fprintf(out, "  • GCS buckets:          KEPT (pass --delete-buckets to remove their contents)\n")
	} else {
		names := make([]string, 0, len(buckets))
		for _, b := range buckets {
			names = append(names, b.Name)
		}
		fmt.Fprintf(out, "  • %d GCS bucket(s):       %s (contents unrecoverable)\n", len(names), orNone(strings.Join(names, ", ")))
	}
	fmt.Fprintf(out, "  • Artifact Registry:    %s\n", orNone(repo))
	fmt.Fprintf(out, "  • project APIs:         left enabled\n")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
