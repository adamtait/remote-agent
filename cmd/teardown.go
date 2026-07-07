package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/gcs"
	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/adamtait/remote-agent/internal/secrets"
	"github.com/spf13/cobra"
)

// totalTeardownSteps is the fixed number of teardown phases, shown as
// "[n/total]" in progress output. Conditional phases (shared resources,
// secrets, buckets) still consume a number and print a skip note when disabled,
// mirroring the fixed step count of `ra create`.
const totalTeardownSteps = 8

// teardownOpts captures the teardown flags.
type teardownOpts struct {
	Force         bool
	DryRun        bool
	DeleteBuckets bool
	KeepSecrets   bool
	DeleteShared  bool
}

var teardownFlags teardownOpts

var teardownCmd = &cobra.Command{
	Use:   "teardown",
	Short: "Delete gcloud resources ra created (workstations, config, and shared cluster/SA/repo/secrets)",
	Long: `Delete GCP resources provisioned by 'ra create', in dependency-safe order.

Per-project resources — this project's workstation instances and its workstations
config — are always deleted. Shared resources — the cluster (which accrues the
fixed control-plane fee), the service account, project IAM bindings, Secret
Manager secrets, and the Artifact Registry repository — default to ra's user-level
names and are typically shared across projects, so they are deleted ONLY when this
is the last config using the cluster (or when --delete-shared is passed).

Project APIs are left enabled (project-wide, unsafe to disable). Plugin GCS buckets
are preserved unless --delete-buckets is passed, because their contents (e.g.
synced work artifacts) are not recoverable.`,
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
	teardownCmd.Flags().BoolVar(&teardownFlags.KeepSecrets, "keep-secrets", false, "Preserve Secret Manager secrets even when deleting shared resources")
	teardownCmd.Flags().BoolVar(&teardownFlags.DeleteShared, "delete-shared", false, "Force deletion of shared cluster/SA/repo/secrets even if other configs still use the cluster")
	addLocationOverrideFlags(teardownCmd)
	rootCmd.AddCommand(teardownCmd)
}

// runTeardown deletes the ra-provisioned resources identified by ref + cfg in
// reverse dependency order.
//
// Because `ra create` provisions the cluster, service account, Artifact
// Registry repo, and secrets under user-level default names shared across
// projects, teardown distinguishes per-project resources (this config and its
// workstation instances — always deleted) from shared resources (deleted only
// when this is the last config on the cluster, or when --delete-shared is set).
// Each resource is deleted only if it exists (check-then-delete), so re-runs
// converge; a delete that fails on an existing resource is collected and
// surfaced at the end without stranding the remaining steps.
func runTeardown(client *gcloud.Client, r gcloud.Runner, cfg *config.Config, reg *plugins.Registry, ref gcloud.WorkstationRef, opts teardownOpts, out io.Writer) error {
	if ref.Project == "" {
		return fmt.Errorf("project not set (config gcloud.project or --project)")
	}

	ws := cfg.Gcloud.Workstation
	saName := ws.ServiceAccountName
	saEmail := serviceAccountEmailFor(saName, ref.Project)
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

	// Decide whether shared resources are safe to delete: only when this is the
	// last config on the cluster (no siblings), or when forced.
	deleteShared, sharedReason := sharedDeletionDecision(client, ref, opts)

	printTeardownPlan(out, ref, saEmail, ws.RepoName, roles, instances, secretNames, buckets, opts, deleteShared, sharedReason)

	if opts.DryRun {
		fmt.Fprintln(out, "\nDry run — no resources were deleted.")
		return nil
	}

	if !opts.Force {
		ok, cerr := confirmYes(fmt.Sprintf("\nDelete the above resources in project %q? This cannot be undone. [y/N]: ", ref.Project))
		if errors.Is(cerr, errStdinNotTerminal) {
			return fmt.Errorf("stdin is not a terminal; re-run with --force to tear down non-interactively")
		}
		if cerr != nil {
			return cerr
		}
		if !ok {
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

	// Step 1 — workstation instances (per-project, always deleted).
	done := logging.Step("teardown_workstations", 1, totalTeardownSteps,
		fmt.Sprintf("Deleting %d workstation instance(s)...", len(instances)), out)
	var stepErr error
	for _, name := range instances {
		if err := client.Delete(ref.Project, ref.Region, ref.Cluster, ref.Config, name); err != nil {
			fail(fmt.Sprintf("workstation %q", name), err)
			stepErr = err
		}
	}
	done(stepErr)

	// Step 2 — workstations config (per-project, always deleted).
	done = logging.Step("teardown_config", 2, totalTeardownSteps, "Deleting workstations config...", out)
	stepErr = nil
	if ref.Cluster != "" && ref.Config != "" &&
		r.Check("workstations", "configs", "describe", ref.Config, "--cluster", ref.Cluster, "--region", ref.Region, "--project", ref.Project) {
		if err := client.DeleteConfig(ref.Project, ref.Region, ref.Cluster, ref.Config); err != nil {
			fail(fmt.Sprintf("config %q", ref.Config), err)
			stepErr = err
		}
	}
	done(stepErr)

	// Step 3 — workstations cluster (SHARED; the control-plane-fee resource).
	done = logging.Step("teardown_cluster", 3, totalTeardownSteps, clusterStepMsg(deleteShared), out)
	stepErr = nil
	if deleteShared && ref.Cluster != "" &&
		r.Check("workstations", "clusters", "describe", ref.Cluster, "--region", ref.Region, "--project", ref.Project) {
		if err := client.DeleteCluster(ref.Project, ref.Region, ref.Cluster); err != nil {
			fail(fmt.Sprintf("cluster %q", ref.Cluster), err)
			stepErr = err
		}
	}
	done(stepErr)

	// Step 4 — project IAM bindings (SHARED; best-effort, since removing an
	// absent binding is harmless there is no existence check).
	done = logging.Step("teardown_project_iam", 4, totalTeardownSteps, iamStepMsg(deleteShared, len(roles)), out)
	if deleteShared && saName != "" {
		for _, role := range roles {
			_ = r.RunSoft("projects", "remove-iam-policy-binding", ref.Project,
				"--member=serviceAccount:"+saEmail, "--role="+role, "--condition=None")
		}
	}
	done(nil)

	// Step 5 — service account (SHARED). Deleting it invalidates its remaining
	// secret/bucket/AR bindings for resources that are also being deleted; note
	// that a preserved secret (--keep-secrets) or a preserved bucket keeps a
	// harmless dangling "deleted:serviceAccount:" binding.
	done = logging.Step("teardown_service_account", 5, totalTeardownSteps, saStepMsg(deleteShared), out)
	stepErr = nil
	if deleteShared && saName != "" &&
		r.Check("iam", "service-accounts", "describe", saEmail, "--project", ref.Project) {
		if err := r.Run("iam", "service-accounts", "delete", saEmail, "--project", ref.Project, "--quiet"); err != nil {
			fail(fmt.Sprintf("service account %q", saEmail), err)
			stepErr = err
		}
	}
	done(stepErr)

	// Step 6 — Secret Manager secrets (SHARED).
	done = logging.Step("teardown_secrets", 6, totalTeardownSteps, secretsStepMsg(deleteShared, opts.KeepSecrets, len(secretNames)), out)
	stepErr = nil
	if deleteShared && !opts.KeepSecrets {
		for _, name := range secretNames {
			if secrets.Exists(r, ref.Project, name) {
				if err := secrets.Delete(r, ref.Project, name); err != nil {
					fail(fmt.Sprintf("secret %q", name), err)
					stepErr = err
				}
			}
		}
	}
	done(stepErr)

	// Step 7 — plugin GCS buckets (opt-in; unrecoverable contents). Buckets are
	// gated on both --delete-buckets and shared-deletion, since a bucket can be
	// shared across a user's projects.
	done = logging.Step("teardown_buckets", 7, totalTeardownSteps, bucketsStepMsg(deleteShared, opts.DeleteBuckets, len(buckets)), out)
	stepErr = nil
	if deleteShared && opts.DeleteBuckets {
		for _, b := range buckets {
			if r.Check("storage", "buckets", "describe", "gs://"+b.Name, "--project", ref.Project) {
				if err := gcs.DeleteBucket(r, ref.Project, b.Name); err != nil {
					fail(fmt.Sprintf("bucket %q", b.Name), err)
					stepErr = err
				}
			}
		}
	}
	done(stepErr)

	// Step 8 — Artifact Registry repository (SHARED; removes images too).
	done = logging.Step("teardown_repo", 8, totalTeardownSteps, repoStepMsg(deleteShared), out)
	stepErr = nil
	if deleteShared && ws.RepoName != "" &&
		r.Check("artifacts", "repositories", "describe", ws.RepoName, "--location", ref.Region, "--project", ref.Project) {
		if err := r.Run("artifacts", "repositories", "delete", ws.RepoName,
			"--location", ref.Region, "--project", ref.Project, "--quiet"); err != nil {
			fail(fmt.Sprintf("repository %q", ws.RepoName), err)
			stepErr = err
		}
	}
	done(stepErr)

	if len(failures) > 0 {
		fmt.Fprintf(out, "\nTeardown finished with %d error(s); re-run to retry the remaining resources.\n", len(failures))
		return errors.Join(failures...)
	}
	if !deleteShared {
		fmt.Fprintf(out, "\nTeardown complete for this config. Shared resources were preserved (%s).\n", sharedReason)
	} else {
		fmt.Fprintln(out, "\nTeardown complete. All ra-provisioned resources removed (APIs left enabled).")
	}
	return nil
}

// sharedDeletionDecision reports whether shared resources (cluster, SA, repo,
// secrets, buckets) should be deleted, and a human-readable reason when they are
// preserved. Shared resources are deleted only when this is the last config on
// the cluster or when --delete-shared is passed. When the config list cannot be
// determined, teardown errs on the side of preserving shared infrastructure.
func sharedDeletionDecision(client *gcloud.Client, ref gcloud.WorkstationRef, opts teardownOpts) (deleteShared bool, reason string) {
	if opts.DeleteShared {
		return true, "forced with --delete-shared"
	}
	if ref.Cluster == "" {
		return false, "no cluster resolved"
	}
	configs, err := client.ListConfigs(ref.Project, ref.Region, ref.Cluster)
	if err != nil {
		return false, fmt.Sprintf("could not list cluster configs (%v); pass --delete-shared to force", err)
	}
	others := otherConfigNames(configs, ref.Config)
	if len(others) == 0 {
		return true, "last config on the cluster"
	}
	return false, fmt.Sprintf("%d other config(s) still use cluster %q: %s", len(others), ref.Cluster, strings.Join(others, ", "))
}

// otherConfigNames returns the basenames of configs that are not self.
func otherConfigNames(configs []string, self string) []string {
	var others []string
	for _, c := range configs {
		if base := path.Base(strings.TrimSpace(c)); base != "" && base != "." && base != self {
			others = append(others, base)
		}
	}
	return others
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

func clusterStepMsg(shared bool) string {
	if !shared {
		return "Deleting workstations cluster... (skipped: shared, preserved)"
	}
	return "Deleting workstations cluster..."
}

func iamStepMsg(shared bool, n int) string {
	if !shared {
		return "Removing project IAM bindings... (skipped: shared, preserved)"
	}
	return fmt.Sprintf("Removing %d project IAM binding(s)...", n)
}

func saStepMsg(shared bool) string {
	if !shared {
		return "Deleting service account... (skipped: shared, preserved)"
	}
	return "Deleting service account..."
}

func secretsStepMsg(shared, keep bool, n int) string {
	switch {
	case !shared:
		return "Deleting secrets... (skipped: shared, preserved)"
	case keep:
		return "Deleting secrets... (skipped: --keep-secrets)"
	default:
		return fmt.Sprintf("Deleting %d secret(s)...", n)
	}
}

func bucketsStepMsg(shared, del bool, n int) string {
	switch {
	case !shared:
		return "Deleting plugin buckets... (skipped: shared, preserved)"
	case !del:
		return "Deleting plugin buckets... (skipped: pass --delete-buckets to remove)"
	default:
		return fmt.Sprintf("Deleting %d plugin bucket(s)...", n)
	}
}

func repoStepMsg(shared bool) string {
	if !shared {
		return "Deleting Artifact Registry repository... (skipped: shared, preserved)"
	}
	return "Deleting Artifact Registry repository..."
}

// printTeardownPlan prints the human-readable list of what teardown will delete,
// separating always-deleted per-project resources from shared resources.
func printTeardownPlan(out io.Writer, ref gcloud.WorkstationRef, saEmail, repo string, roles, instances, secretNames []string, buckets []resolvedBucket, opts teardownOpts, deleteShared bool, sharedReason string) {
	fmt.Fprintf(out, "Teardown plan for project %q (region %s):\n", ref.Project, ref.Region)
	fmt.Fprintln(out, "  Per-project (always deleted):")
	if len(instances) > 0 {
		fmt.Fprintf(out, "    • %d workstation instance(s): %s\n", len(instances), strings.Join(instances, ", "))
	} else {
		fmt.Fprintf(out, "    • workstation instances: none found\n")
	}
	fmt.Fprintf(out, "    • workstations config: %s\n", orNone(ref.Config))

	if deleteShared {
		fmt.Fprintf(out, "  Shared (deleted — %s):\n", sharedReason)
	} else {
		fmt.Fprintf(out, "  Shared (PRESERVED — %s; pass --delete-shared to force):\n", sharedReason)
	}
	fmt.Fprintf(out, "    • workstations cluster: %s\n", orNone(ref.Cluster))
	fmt.Fprintf(out, "    • service account:      %s\n", saEmail)
	fmt.Fprintf(out, "    • project IAM bindings: %s\n", strings.Join(roles, ", "))
	if opts.KeepSecrets {
		fmt.Fprintf(out, "    • secrets:              KEPT (--keep-secrets)\n")
	} else if len(secretNames) > 0 {
		fmt.Fprintf(out, "    • %d secret(s):          %s\n", len(secretNames), strings.Join(secretNames, ", "))
	} else {
		fmt.Fprintf(out, "    • secrets:              none configured\n")
	}
	if !opts.DeleteBuckets {
		fmt.Fprintf(out, "    • GCS buckets:          KEPT (pass --delete-buckets to remove their contents)\n")
	} else {
		names := make([]string, 0, len(buckets))
		for _, b := range buckets {
			names = append(names, b.Name)
		}
		fmt.Fprintf(out, "    • %d GCS bucket(s):       %s (contents unrecoverable)\n", len(names), orNone(strings.Join(names, ", ")))
	}
	fmt.Fprintf(out, "    • Artifact Registry:    %s\n", orNone(repo))
	fmt.Fprintf(out, "  project APIs: left enabled\n")
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
