package cmd

import (
	"crypto/sha1"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/configwrite"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/gcs"
	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/naming"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/adamtait/remote-agent/internal/scaffold"
	"github.com/adamtait/remote-agent/internal/secrets"
	"github.com/spf13/cobra"
)

// totalCreateSteps is the count of numbered pipeline phases in runCreate,
// rendered in the "==> [N/M] ..." progress prefix. Update if you add or
// remove a Step() call.
const totalCreateSteps = 11

// Bindings on a freshly-created service account can briefly fail with
// "Service account ... does not exist" while IAM propagates. Up to
// saBindingAttempts × saBindingDelay = 30s of backoff covers the typical
// window.
const (
	saBindingAttempts = 6
	saBindingDelay    = 5 * time.Second
)

// baseProjectRoles are the always-on project-level IAM roles `ra create` grants
// the workstation service account (in addition to any plugin auth-provider
// roles). This is the single source of truth; `ra teardown` removes the same
// set, so keeping it here prevents the two from drifting.
var baseProjectRoles = []string{"roles/logging.logWriter", "roles/monitoring.metricWriter"}

// serviceAccountEmailFor returns the email of the workstation service account
// for the given short name and project. Shared by create and teardown so both
// compute the same address.
func serviceAccountEmailFor(saName, project string) string {
	return fmt.Sprintf("%s@%s.iam.gserviceaccount.com", saName, project)
}

func logicalNameToEnvToken(logical string) string {
	return strings.ToUpper(logical)
}

// idSafeRe matches one run of characters that are NOT safe in a GCS
// object-prefix / filesystem path component. Everything outside
// [a-z0-9_-] is collapsed to a single hyphen by stableWorkstationID so
// the id stays a single, slash-free, lowercase path segment.
var idSafeRe = regexp.MustCompile(`[^a-z0-9_-]+`)

// stableWorkstationID derives a deterministic identity token from inputs
// that survive `ra create` rebuilds — the gcloud project, the workstation
// config_name, and the git user email. Unlike the GCE instance name
// (which is a fresh UUID every create), these are stable for a given
// (project, config, user) triple, so a plugin keyed on this token reuses
// ONE per-workstation identity across rebuilds instead of accumulating a
// fresh one each time. Plugins opt in via `stable_id:` in plugin.yaml.
//
// The output is lowercased, restricted to [a-z0-9_-], slash-free, and
// suffixed with an 8-hex digest of the raw triple so two triples that
// collapse to the same sanitized stem (e.g. emails differing only in
// punctuation) still get distinct ids. Returns "" if all three inputs are
// empty, so the caller can skip injection and let the plugin fall back to
// its own default.
func stableWorkstationID(project, configName, userEmail string) string {
	p := strings.ToLower(strings.TrimSpace(project))
	c := strings.ToLower(strings.TrimSpace(configName))
	e := strings.ToLower(strings.TrimSpace(userEmail))
	raw := p + "/" + c + "/" + e
	if raw == "//" {
		return ""
	}
	sum := sha1.Sum([]byte(raw))
	// Build the human-readable stem from the already-normalized
	// components so re-cased / space-padded inputs produce the identical
	// stem (not just the identical digest).
	stem := p + "-" + c
	stem = idSafeRe.ReplaceAllString(stem, "-")
	stem = strings.Trim(stem, "-_")
	if stem == "" {
		stem = "ws"
	}
	// Keep the human-readable stem short so the full id stays a tidy path
	// segment; the digest carries the uniqueness guarantee.
	if len(stem) > 40 {
		stem = strings.Trim(stem[:40], "-_")
	}
	return fmt.Sprintf("%s-%x", stem, sum[:4])
}

// resolvedBucket pairs a plugin's BucketSpec declaration with the
// concrete bucket name produced by resolving its template against the
// active gcloud project + git user email (or the user-supplied
// override). Returned by pluginBuckets() and consumed by both the
// provisioning step and the env-injection in buildContainerEnv —
// keeping the source of truth in plugin.yaml, not in cmd/create.go.
type resolvedBucket struct {
	Plugin string             // plugin name (for log/error messages only)
	Spec   plugins.BucketSpec // verbatim from plugin.yaml
	Name   string             // resolved + validated GCS bucket name
}

// pluginBuckets enumerates every BucketSpec declared by every installed
// plugin that has `enabled: true`, resolving each one's name template
// (or honoring its override_field) and validating the result. Returns
// an error if any plugin's bucket cannot be resolved, so `ra create`
// fails fast before provisioning anything.
func pluginBuckets(reg *plugins.Registry, cfg *config.Config) ([]resolvedBucket, error) {
	if reg == nil {
		return nil, nil
	}
	var out []resolvedBucket
	for _, p := range reg.All() {
		schema := p.Schema()
		if len(schema.Buckets) == 0 {
			continue
		}
		pluginCfg := cfg.Plugins[p.Name()]
		// Honor the existing `enabled: true` convention — plugin-side
		// startup scripts also gate on RA_PLUGIN_<NAME>_ENABLED, so a
		// disabled plugin contributes no buckets here.
		if pluginCfg == nil {
			continue
		}
		if en, _ := pluginCfg["enabled"].(bool); !en {
			continue
		}
		for _, spec := range schema.Buckets {
			name := ""
			if spec.OverrideField != "" {
				if v, _ := pluginCfg[spec.OverrideField].(string); v != "" {
					name = v
				}
			}
			if name == "" {
				resolved, err := gcs.ResolveBucketName(spec.NameTemplate, cfg.Gcloud.Project, cfg.Git.UserEmail)
				if err != nil {
					return nil, fmt.Errorf("plugin %s bucket %q: %w", p.Name(), spec.Name, err)
				}
				name = resolved
			}
			if err := gcs.ValidateBucketName(name); err != nil {
				return nil, fmt.Errorf("plugin %s bucket %q: %w", p.Name(), spec.Name, err)
			}
			out = append(out, resolvedBucket{Plugin: p.Name(), Spec: spec, Name: name})
		}
	}
	return out, nil
}

// lifecycleRulesFor converts a plugin's declared lifecycle rules
// (plugins.LifecycleRule, parsed from plugin.yaml) into the gcs package's
// rule type. The two are kept as separate types so internal/gcs need not
// import internal/plugins; this is the single conversion seam.
func lifecycleRulesFor(spec plugins.BucketSpec) []gcs.LifecycleRule {
	if len(spec.Lifecycle) == 0 {
		return nil
	}
	out := make([]gcs.LifecycleRule, 0, len(spec.Lifecycle))
	for _, lr := range spec.Lifecycle {
		out = append(out, gcs.LifecycleRule{
			Action:       lr.Action,
			StorageClass: lr.StorageClass,
			AgeDays:      lr.AgeDays,
			Prefixes:     lr.Prefixes,
		})
	}
	return out
}

// uniqueBucketsWithLifecycle collapses resolved buckets to their unique
// names in first-seen order and unions each name's declared lifecycle
// rules. Because two plugins can share a bucket and ApplyLifecycle
// replaces the whole lifecycle config per call, the rules must be unioned
// and applied once per bucket rather than per plugin (which would let the
// last plugin clobber the others). EnsureBucket likewise only needs
// calling once per unique name.
func uniqueBucketsWithLifecycle(buckets []resolvedBucket) (order []string, lifecycle map[string][]gcs.LifecycleRule) {
	lifecycle = map[string][]gcs.LifecycleRule{}
	seen := map[string]bool{}
	for _, b := range buckets {
		if !seen[b.Name] {
			seen[b.Name] = true
			order = append(order, b.Name)
		}
		lifecycle[b.Name] = append(lifecycle[b.Name], lifecycleRulesFor(b.Spec)...)
	}
	return order, lifecycle
}

// softDeleteDaysByBucket returns, per unique bucket name, the largest
// soft_delete_days any plugin sharing that bucket declares. Buckets whose
// plugins declare none are absent, so their existing policy is left alone.
// Max (not last-wins) so one plugin can never shrink another's recovery
// window on a shared bucket.
func softDeleteDaysByBucket(buckets []resolvedBucket) map[string]int {
	out := map[string]int{}
	for _, b := range buckets {
		if b.Spec.SoftDeleteDays == nil {
			continue
		}
		if cur, ok := out[b.Name]; !ok || *b.Spec.SoftDeleteDays > cur {
			out[b.Name] = *b.Spec.SoftDeleteDays
		}
	}
	return out
}

func buildContainerEnv(cfg *config.Config) string {
	var pairs []string
	// propagate is the explicit allowlist of keys that 200_remote-agent-setup.sh
	// re-exports into /etc/environment and /run/ra/env so sshd login shells
	// (which do NOT inherit PID-1 env) can see them. We exclude the RA_SECRETS
	// bookkeeping keys because their fetched values are exported under their
	// target_env_var names by the secret-fetch loop already.
	var propagate []string
	add := func(k, v string) { pairs = append(pairs, k+"="+v) }
	addProp := func(k, v string) {
		pairs = append(pairs, k+"="+v)
		propagate = append(propagate, k)
	}

	addProp("GCP_PROJECT_ID", cfg.Gcloud.Project)
	addProp("GIT_USER_NAME", cfg.Git.UserName)
	addProp("GIT_USER_EMAIL", cfg.Git.UserEmail)
	// Plugin-declared GCS buckets (in plugin.yaml `buckets:`) are
	// resolved at create-time — the names depend on gcloud project + git
	// user email, not on static plugin config — so inject them here in
	// addition to the plugin's own config fields that flow through
	// reg.AllContainerEnvPairs below. pluginBuckets returns an error
	// only on invalid template/override, which we've already surfaced
	// up the pipeline; tolerate it here so buildContainerEnv stays
	// total.
	if buckets, err := pluginBuckets(reg, cfg); err == nil {
		for _, b := range buckets {
			addProp(b.Spec.EnvVar, b.Name)
		}
	}
	// Stable workstation-identity injection. A plugin may declare
	// `stable_id: { env_var: ... }` in plugin.yaml to receive a
	// deterministic token (see stableWorkstationID) that survives `ra
	// create` rebuilds, unlike the GCE instance name. Generic, like the
	// bucket env injection above: core hard-codes no plugin name; the
	// env-var name comes from the plugin's own declaration. Gated on
	// `enabled: true` and added to the propagate allowlist so login shells
	// see it. The token depends on create-time inputs (project, config,
	// user) that the static plugin-config path cannot express, which is why
	// it is computed here rather than flowing through reg.AllContainerEnvPairs.
	if reg != nil {
		if id := stableWorkstationID(cfg.Gcloud.Project, cfg.Gcloud.Workstation.ConfigName, cfg.Git.UserEmail); id != "" {
			for _, p := range reg.All() {
				spec := p.Schema().StableID
				if spec == nil || spec.EnvVar == "" {
					continue
				}
				pc := cfg.Plugins[p.Name()]
				if pc == nil {
					continue
				}
				if en, _ := pc["enabled"].(bool); !en {
					continue
				}
				addProp(spec.EnvVar, id)
			}
		}
	}
	for _, pair := range reg.AllContainerEnvPairs(cfg) {
		pairs = append(pairs, pair)
		if eq := strings.IndexByte(pair, '='); eq > 0 {
			propagate = append(propagate, pair[:eq])
		}
	}

	logicalNames := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		logicalNames = append(logicalNames, k)
	}
	sort.Strings(logicalNames)
	add("RA_SECRETS", strings.Join(logicalNames, "|"))
	for _, logical := range logicalNames {
		s := cfg.Secrets[logical]
		tok := logicalNameToEnvToken(logical)
		add("RA_SECRET_"+tok+"_NAME", s.SecretName)
		add("RA_SECRET_"+tok+"_VAR", s.EnvVar)
	}

	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		addProp(k, cfg.Env[k])
	}

	add("RA_PROPAGATE_KEYS", strings.Join(propagate, "|"))

	return strings.Join(pairs, ",")
}

var createCmd = &cobra.Command{
	Use:   "create [name]",
	Short: "Provision a Google Cloud Workstation",
	Long: `Always provisions a new workstation. Cluster, config, image, secrets,
and IAM setup remain idempotent; the workstation itself is freshly created on
every run. Pass [name] to set the workstation name (auto-suffixed on collision);
omit it for an auto-generated memorable name.
Requires .ra/config.yaml in the project root with gcloud.project set.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runCreate,
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().Int("auto-sleep", 0, "Hours idle before stopping (0=never sleep, omit=GCP default ~2h; max 24)")
	createCmd.Flags().Int("auto-shutdown", 0, "Hours running before force-stop (omit=GCP default ~12h; 1–168)")
}

func runCreate(cmd *cobra.Command, args []string) (retErr error) {
	startedAt := time.Now()
	ws := &cfg.Gcloud.Workstation

	var requestedName string
	if len(args) == 1 {
		requestedName = args[0]
	}

	for field, val := range map[string]string{
		"git_user_name":  cfg.Git.UserName,
		"git_user_email": cfg.Git.UserEmail,
	} {
		if strings.ContainsAny(val, ",=") {
			return fmt.Errorf("%s must not contain ',' or '=' (gcloud --container-env limitation): %q", field, val)
		}
	}

	if cmd.Flags().Changed("auto-sleep") {
		v, _ := cmd.Flags().GetInt("auto-sleep")
		ws.AutoSleepHours = &v
	}
	if cmd.Flags().Changed("auto-shutdown") {
		v, _ := cmd.Flags().GetInt("auto-shutdown")
		ws.AutoShutdownHours = v
	}

	if ws.AutoSleepHours != nil {
		h := *ws.AutoSleepHours
		if h < 0 || h > 24 {
			return fmt.Errorf("auto_sleep_hours must be 0 (never) or 1–24; got %d", h)
		}
	}
	if ws.AutoShutdownHours != 0 {
		if ws.AutoShutdownHours < 1 || ws.AutoShutdownHours > 168 {
			return fmt.Errorf("auto_shutdown_hours must be 1–168; got %d", ws.AutoShutdownHours)
		}
	}

	// Auto-generate a unique config_name per project if none is set at the project level.
	// This scopes workstation listings to this project so `ra connect` finds the right one.
	if cfg.ProjectConfigPath != "" {
		if rawProject, err := config.LoadProjectConfig(cfg.ProjectConfigPath); err == nil &&
			rawProject.Gcloud.Workstation.ConfigName == "" {
			repoDir := filepath.Dir(filepath.Dir(cfg.ProjectConfigPath))
			configName := config.ConfigName(filepath.Base(repoDir))
			if origBytes, readErr := os.ReadFile(cfg.ProjectConfigPath); readErr == nil {
				updated, writeErr := configwrite.MergeConfigName(string(origBytes), configName)
				if writeErr == nil {
					writeErr = os.WriteFile(cfg.ProjectConfigPath, []byte(updated), 0644)
				}
				if writeErr != nil {
					fmt.Printf("\033[1;33mWARN:\033[0m could not save config_name to project config: %v\n", writeErr)
				} else {
					ws.ConfigName = configName
				}
			}
		}
	}

	r := runner

	imageURI := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s",
		ws.Region, cfg.Gcloud.Project, ws.RepoName, ws.ImageName, ws.ImageTag)
	serviceAccountEmail := serviceAccountEmailFor(ws.ServiceAccountName, cfg.Gcloud.Project)

	// Stable order for secret iteration — used by pre-flight, step 4, and step 5.
	secretLogicalOrder := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		secretLogicalOrder = append(secretLogicalOrder, k)
	}
	sort.Strings(secretLogicalOrder)

	// Pipeline-failure resume hint. Registered after validation/config
	// setup so flag-validation errors (e.g. bad auto_sleep_hours) do not
	// get the misleading "earlier steps are idempotent" suffix.
	defer func() {
		if retErr == nil {
			return
		}
		hint := "re-run `ra create"
		if requestedName != "" {
			hint += " " + requestedName
		}
		hint += "` to resume — earlier steps are idempotent."
		fmt.Fprintf(cmd.OutOrStderr(), "\n%s\n", hint)
	}()

	// Pre-flight — collect values for any missing secrets up front so the
	// pipeline runs unattended once it starts. (Inline prompts mid-pipeline
	// force the user to babysit a 30s+ image build before they're asked.)
	type pendingSecret struct {
		gcpName string
		value   []byte
	}
	var pendingSecrets []pendingSecret
	// Register the zeroing defer before the prompt loop so values from
	// successful prompts are still zeroed if a later prompt errors.
	defer func() {
		for i := range pendingSecrets {
			for j := range pendingSecrets[i].value {
				pendingSecrets[i].value[j] = 0
			}
		}
	}()
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		if secrets.Exists(r, cfg.Gcloud.Project, s.SecretName) {
			fmt.Fprintf(os.Stderr, "Secret %q already exists (use 'gcloud secrets versions add' to rotate)\n", s.SecretName)
			continue
		}
		// Missing in the target project — typically because the user
		// switched gcloud.project after `ra plugin install` uploaded the
		// secret to a different project. Look up the owning plugin so
		// codex-style value_command flows (e.g. `codex login`) re-run
		// instead of falling back to a plain text prompt that can't
		// produce the right value.
		plugin, def, ok := reg.FindSecretOwner(cfg, logical)
		if !ok {
			return fmt.Errorf("secret %q is referenced in config but no installed plugin owns it; "+
				"run `ra plugin install <name>` to (re)install the plugin, or remove the orphaned entry from config.yaml",
				logical)
		}
		val, err := plugins.CollectSecretValue(os.Stdin, os.Stderr, *def)
		if err != nil {
			return fmt.Errorf("collecting value for secret %s (plugin %s): %w", s.SecretName, plugin.Name(), err)
		}
		pendingSecrets = append(pendingSecrets, pendingSecret{
			gcpName: s.SecretName,
			value:   []byte(val),
		})
	}

	// Step 1 — Enable APIs
	done := logging.Step("enable_apis", 1, totalCreateSteps, "Enabling required GCP APIs...", cmd.OutOrStdout())
	if err := r.Run("services", "enable",
		"workstations.googleapis.com",
		"artifactregistry.googleapis.com",
		"secretmanager.googleapis.com",
		"iam.googleapis.com",
		"--project", cfg.Gcloud.Project, "--quiet"); err != nil {
		done(err)
		return fmt.Errorf("enabling APIs: %w", err)
	}
	done(nil)

	// reauth.googleapis.com is only enableable by orgs that have been
	// allowlisted for it; on other projects Service Usage rejects it with
	// "not available to this consumer". It's only needed when the caller's
	// org enforces a session-length/reauth policy — gcloud calls it during
	// OAuth token refresh, and commands like `gcloud workstations ssh` fail
	// with SERVICE_DISABLED if that policy is on and the API is off. Try
	// best-effort; surface a warning but don't block the rest of setup.
	if err := r.RunSoft("services", "enable", "reauth.googleapis.com",
		"--project", cfg.Gcloud.Project, "--quiet"); err != nil {
		logging.WarnPrintf(cmd.OutOrStdout(), "could not enable reauth.googleapis.com (%v).\n"+
			"      This is only required if your org enforces a session/reauth policy.\n"+
			"      If `gcloud workstations ssh` later fails with SERVICE_DISABLED for\n"+
			"      reauth.googleapis.com, ask an org admin to enable it.", err)
	}

	// Step 2 — Artifact Registry repo
	done = logging.Step("ensure_ar_repo", 2, totalCreateSteps, fmt.Sprintf("Ensuring Artifact Registry repo '%s'...", ws.RepoName), cmd.OutOrStdout())
	if !r.Check("artifacts", "repositories", "describe", ws.RepoName, "--location", ws.Region, "--project", cfg.Gcloud.Project) {
		if err := r.Run("artifacts", "repositories", "create", ws.RepoName,
			"--repository-format=docker",
			"--location", ws.Region,
			"--description=ra workstation images",
			"--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("creating artifact registry repo: %w", err)
		}
	}
	done(nil)

	// Step 3 — Plugin-declared GCS buckets.
	// Each installed+enabled plugin can declare `buckets:` in
	// plugin.yaml; ra walks them all generically here. No plugin name
	// is hard-coded — any plugin that needs a bucket lands here
	// automatically. IAM binding is deferred to the service-account
	// step below since the SA may not yet exist.
	pluginBucketList, err := pluginBuckets(reg, cfg)
	if err != nil {
		return fmt.Errorf("resolving plugin buckets: %w", err)
	}
	if len(pluginBucketList) > 0 {
		names := make([]string, 0, len(pluginBucketList))
		for _, b := range pluginBucketList {
			names = append(names, b.Name)
		}
		done = logging.Step("ensure_plugin_buckets", 3, totalCreateSteps, fmt.Sprintf("Ensuring %d plugin-declared GCS bucket(s): %s...", len(pluginBucketList), strings.Join(names, ", ")), cmd.OutOrStdout())
		// Two plugins may resolve to the SAME bucket (the override_field
		// mechanism exists precisely so they can share one). Ensure each
		// unique bucket once, then apply the UNION of every plugin's
		// declared lifecycle rules per bucket — ApplyLifecycle replaces the
		// bucket's whole lifecycle config per call, so applying per-plugin
		// would let the last plugin silently clobber the others' rules.
		bucketOrder, lifecycleByBucket := uniqueBucketsWithLifecycle(pluginBucketList)
		firstPlugin := map[string]string{}
		for _, b := range pluginBucketList {
			if _, ok := firstPlugin[b.Name]; !ok {
				firstPlugin[b.Name] = b.Plugin
			}
		}
		for _, name := range bucketOrder {
			if err := gcs.EnsureBucket(r, cfg.Gcloud.Project, name, ws.Region); err != nil {
				done(err)
				return fmt.Errorf("ensuring bucket %s (plugin %s): %w", name, firstPlugin[name], err)
			}
		}
		// Apply accumulated Object Lifecycle Management rules once per
		// bucket. GCS replaces the whole lifecycle config each call, so this
		// re-asserts the policy idempotently on every `ra create`. No-op for
		// buckets whose plugins declare none.
		for _, name := range bucketOrder {
			rules := lifecycleByBucket[name]
			if len(rules) == 0 {
				continue
			}
			if err := gcs.ApplyLifecycle(r, cfg.Gcloud.Project, name, rules); err != nil {
				done(err)
				return fmt.Errorf("applying lifecycle to bucket %s: %w", name, err)
			}
		}
		// Assert declared soft-delete retention once per bucket (largest
		// value across sharing plugins). Skipped for undeclared buckets.
		softDelete := softDeleteDaysByBucket(pluginBucketList)
		for _, name := range bucketOrder {
			days, ok := softDelete[name]
			if !ok {
				continue
			}
			if err := gcs.ApplySoftDelete(r, cfg.Gcloud.Project, name, days); err != nil {
				done(err)
				return fmt.Errorf("applying soft delete to bucket %s: %w", name, err)
			}
		}
		done(nil)
	} else {
		// Placeholder so the visible step counter stays monotonic
		// even on workstations whose plugins don't declare buckets.
		done = logging.Step("ensure_plugin_buckets", 3, totalCreateSteps, "no plugin-declared GCS buckets", cmd.OutOrStdout())
		done(nil)
	}

	// Step 4 — Build and push image
	done = logging.Step("build_image", 4, totalCreateSteps, fmt.Sprintf("Building image %s...", imageURI), cmd.OutOrStdout())

	home, err := os.UserHomeDir()
	if err != nil {
		done(err)
		return fmt.Errorf("determining home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		done(err)
		return fmt.Errorf("determining working directory: %w", err)
	}
	buildBaseName := requestedName
	if buildBaseName == "" {
		buildBaseName = cfg.WorkstationName
	}
	buildDir, err := makeBuildDir(home, buildBaseName)
	if err != nil {
		done(err)
		return err
	}
	buildPath, buildCleanup, err := scaffold.AssembleBuildContext(scaffold.BuildContextOptions{
		Dest:    buildDir,
		Cwd:     cwd,
		Home:    home,
		Plugins: reg.All(),
		ErrOut:  cmd.OutOrStdout(),
	})
	if err != nil {
		done(err)
		return fmt.Errorf("assembling build context: %w", err)
	}
	defer buildCleanup()

	if err := r.Run("auth", "configure-docker", fmt.Sprintf("%s-docker.pkg.dev", ws.Region), "--quiet"); err != nil {
		done(err)
		return fmt.Errorf("configuring docker auth: %w", err)
	}
	if err := (dockerRunner{stdout: cmd.OutOrStdout(), stderr: cmd.ErrOrStderr()}).Run(dockerBuildArgs(imageURI, buildPath)...); err != nil {
		done(err)
		return fmt.Errorf("building and pushing image: %w", err)
	}
	done(nil)

	// Step 5 — Create any secrets whose values we collected during pre-flight.
	// Existing secrets were skipped already; this loop only writes new ones.
	done = logging.Step("ensure_secrets", 5, totalCreateSteps, fmt.Sprintf("Creating %d new GCP secret(s)...", len(pendingSecrets)), cmd.OutOrStdout())
	for _, ps := range pendingSecrets {
		if err := secrets.CreateFromValue(r, cfg.Gcloud.Project, ps.gcpName, ps.value); err != nil {
			done(err)
			return fmt.Errorf("creating secret %s: %w", ps.gcpName, err)
		}
	}
	done(nil)

	// Step 6 — Service account and IAM
	done = logging.Step("ensure_service_acct", 6, totalCreateSteps, fmt.Sprintf("Ensuring service account '%s'...", serviceAccountEmail), cmd.OutOrStdout())
	if !r.Check("iam", "service-accounts", "describe", serviceAccountEmail, "--project", cfg.Gcloud.Project) {
		if err := r.Run("iam", "service-accounts", "create", ws.ServiceAccountName,
			"--display-name=ra Workstation SA",
			"--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("creating service account: %w", err)
		}
	}

	// Bind secrets
	secretNames := make([]string, 0, len(cfg.Secrets))
	seen := map[string]bool{}
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		if !seen[s.SecretName] {
			secretNames = append(secretNames, s.SecretName)
			seen[s.SecretName] = true
		}
	}
	// IAM bindings on a freshly-created service account can race
	// gcloud's eventual-consistency window and fail with "Service
	// account ... does not exist". RetryOnSANotFound waits it out.
	for _, secret := range secretNames {
		if err := gcloud.RetryOnSANotFound(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
			"secrets", "add-iam-policy-binding", secret,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role=roles/secretmanager.secretAccessor",
			"--project", cfg.Gcloud.Project,
			"--condition=None"); err != nil {
			done(err)
			return fmt.Errorf("binding secret %s IAM: %w", secret, err)
		}
	}

	// Bind plugin-declared bucket IAM. Each plugin's BucketSpec carries
	// its own `role:` (typically roles/storage.objectAdmin for a read+
	// write plugin; could be a viewer role for a read-only one). Same
	// RetryOnSANotFound pattern as the secret bindings to cover the IAM
	// eventual-consistency window after a fresh service-account create.
	for _, b := range pluginBucketList {
		if err := gcs.GrantRole(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
			b.Name, serviceAccountEmail, b.Spec.Role); err != nil {
			done(err)
			return fmt.Errorf("binding %s on bucket %s (plugin %s): %w", b.Spec.Role, b.Name, b.Plugin, err)
		}
	}
	done(nil)

	// Bind Artifact Registry repo
	done = logging.Step("bind_ar_iam", 7, totalCreateSteps, fmt.Sprintf("Granting artifactregistry.reader on '%s'...", ws.RepoName), cmd.OutOrStdout())
	if err := gcloud.RetryOnSANotFound(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
		"artifacts", "repositories", "add-iam-policy-binding", ws.RepoName,
		"--location", ws.Region,
		"--member=serviceAccount:"+serviceAccountEmail,
		"--role=roles/artifactregistry.reader",
		"--project", cfg.Gcloud.Project,
		"--condition=None"); err != nil {
		done(err)
		return fmt.Errorf("binding artifact registry IAM: %w", err)
	}
	done(nil)

	// Bind project-level roles. Includes the always-on observability roles
	// plus any project IAM roles declared on each installed plugin's
	// selected auth_provider (e.g. roles/aiplatform.user when a plugin
	// authenticates via Vertex AI using this same service account).
	projectRoles := appendUniqueRoles(append([]string{}, baseProjectRoles...), pluginAuthIAMRoles(reg.All(), cfg))
	done = logging.Step("bind_project_iam", 8, totalCreateSteps, fmt.Sprintf("Granting %d project-level IAM role(s)...", len(projectRoles)), cmd.OutOrStdout())
	for _, role := range projectRoles {
		if err := gcloud.RetryOnSANotFound(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
			"projects", "add-iam-policy-binding", cfg.Gcloud.Project,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role="+role,
			"--condition=None"); err != nil {
			done(err)
			return fmt.Errorf("binding project role %s: %w", role, err)
		}
	}
	done(nil)

	// Step 9 — Workstations cluster
	done = logging.Step("ensure_cluster", 9, totalCreateSteps, fmt.Sprintf("Ensuring workstations cluster '%s'...", ws.ClusterName), cmd.OutOrStdout())
	if !r.Check("workstations", "clusters", "describe", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project) {
		logging.WarnPrintf(cmd.OutOrStdout(), "Creating cluster — this takes ~20 minutes on first run.")
		if err := r.Run("workstations", "clusters", "create", ws.ClusterName,
			"--region", ws.Region,
			"--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("creating workstations cluster: %w", err)
		}
	}
	done(nil)

	// Step 10 — Workstations config
	done = logging.Step("ensure_config", 10, totalCreateSteps, fmt.Sprintf("Ensuring workstations config '%s'...", ws.ConfigName), cmd.OutOrStdout())
	containerEnv := buildContainerEnv(cfg)

	configExists := r.Check("workstations", "configs", "describe", ws.ConfigName,
		"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project)

	if configExists {
		logging.StepPrintf(cmd.OutOrStdout(), "Config exists — updating image, env, service account, and scopes...")
		updateArgs := []string{
			"workstations", "configs", "update", ws.ConfigName,
			"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project,
			"--container-custom-image=" + imageURI,
			"--service-account=" + serviceAccountEmail,
			"--service-account-scopes=https://www.googleapis.com/auth/cloud-platform",
			"--container-env=" + containerEnv,
		}
		if ws.AutoSleepHours != nil {
			updateArgs = append(updateArgs, fmt.Sprintf("--idle-timeout=%d", *ws.AutoSleepHours*3600))
		}
		if ws.AutoShutdownHours > 0 {
			updateArgs = append(updateArgs, fmt.Sprintf("--running-timeout=%d", ws.AutoShutdownHours*3600))
		}
		if err := r.Run(updateArgs...); err != nil {
			done(err)
			return fmt.Errorf("updating workstations config: %w", err)
		}
	} else {
		createArgs := []string{
			"workstations", "configs", "create", ws.ConfigName,
			"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project,
			"--machine-type=" + ws.MachineType,
			"--pd-disk-size=" + strconv.Itoa(ws.DiskSizeGB),
			"--pd-disk-type=pd-balanced",
			"--container-custom-image=" + imageURI,
			"--service-account=" + serviceAccountEmail,
			"--service-account-scopes=https://www.googleapis.com/auth/cloud-platform",
			"--container-env=" + containerEnv,
		}
		if ws.AutoSleepHours != nil {
			createArgs = append(createArgs, fmt.Sprintf("--idle-timeout=%d", *ws.AutoSleepHours*3600))
		}
		if ws.AutoShutdownHours > 0 {
			createArgs = append(createArgs, fmt.Sprintf("--running-timeout=%d", ws.AutoShutdownHours*3600))
		}
		if err := r.Run(createArgs...); err != nil {
			done(err)
			return fmt.Errorf("creating workstations config: %w", err)
		}
	}
	done(nil)

	// Step 11 — Resolve a unique workstation name and create it.
	done = logging.Step("create_workstation", 11, totalCreateSteps, "Resolving workstation name and creating...", cmd.OutOrStdout())
	existing, err := client.List(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName)
	if err != nil {
		done(err)
		return fmt.Errorf("listing existing workstations: %w", err)
	}
	taken := make(map[string]bool, len(existing))
	for _, w := range existing {
		taken[w.Name] = true
	}

	var workstationName string
	if requestedName != "" {
		workstationName = naming.UniqueSuffixed(requestedName, taken)
		if workstationName != requestedName {
			logging.WarnPrintf(cmd.OutOrStdout(), "workstation %q exists — using %q instead", requestedName, workstationName)
		}
	} else {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		workstationName, err = naming.GenerateMemorable(cfg.WorkstationName, taken, rng)
		if err != nil {
			done(err)
			return fmt.Errorf("generating workstation name: %w", err)
		}
	}
	if err := r.Run("workstations", "create", workstationName,
		"--cluster", ws.ClusterName, "--config", ws.ConfigName,
		"--region", ws.Region, "--project", cfg.Gcloud.Project); err != nil {
		done(err)
		return fmt.Errorf("creating workstation: %w", err)
	}

	wsInfo, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, workstationName)
	if err != nil {
		done(err)
		return fmt.Errorf("describing workstation: %w", err)
	}
	if wsInfo.State != "STATE_RUNNING" && wsInfo.State != "STATE_STARTING" {
		logging.StepPrintf(cmd.OutOrStdout(), "Starting workstation...")
		if err := r.Run("workstations", "start", workstationName,
			"--cluster", ws.ClusterName, "--config", ws.ConfigName,
			"--region", ws.Region, "--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("starting workstation: %w", err)
		}
	}
	done(nil)

	// Print access info (post-pipeline; not counted in totalCreateSteps).
	info, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, workstationName)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), `
================================================================
  Workstation is ready
================================================================
  Browser IDE  : https://80-%s
  Workstation  : %s
  Cluster      : %s
  Config       : %s
  Region       : %s

  SSH:
    ra connect %s

  Stop (save cost):
    ra stop %s
================================================================
`,
		info.Host,
		workstationName,
		ws.ClusterName,
		ws.ConfigName,
		ws.Region,
		workstationName,
		workstationName,
	)

	fmt.Fprintf(cmd.OutOrStdout(), "Total time: %s\n", elapsedHuman(time.Since(startedAt)))
	if p := logging.LogPath(); p != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Log: %s\n", p)
	}

	return nil
}

// elapsedHuman formats a duration as human-friendly "MmSSs" or "Ss" with
// sub-second resolution truncated (not rounded — 4m11.9s reads as 4m11s,
// matching what most stopwatches show). Examples: 4m12s, 12s, 1h3m7s.
func elapsedHuman(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	s := int(d / time.Second)
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// dockerRunner runs docker commands with stdout/stderr forwarded to the provided writers.
type dockerRunner struct {
	stdout io.Writer
	stderr io.Writer
}

func (d dockerRunner) Run(args ...string) error {
	start := time.Now()
	logging.L().Debug("docker starting", "args", args)
	c := exec.Command("docker", args...)
	c.Stdout = d.stdout
	c.Stderr = d.stderr
	err := c.Run()
	dur := time.Since(start)
	if err != nil {
		logging.L().Error("docker failed", "duration_s", dur.Seconds(), "error", err.Error())
	} else {
		logging.L().Debug("docker ok", "duration_s", dur.Seconds())
	}
	return err
}

// dockerBuildArgs returns the buildx args used by `ra create`, centralized so
// tests can assert on the exact flags. The Dockerfile is harness-agnostic at
// build time — plugin-specific install steps are gated at runtime by their
// own `RA_PLUGIN_<NAME>_ENABLED` env vars emitted by each plugin.
func dockerBuildArgs(imageURI, buildContext string) []string {
	return []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", imageURI,
		"-f", filepath.Join(buildContext, "Dockerfile"),
		"--push",
		buildContext,
	}
}

// pluginAuthIAMRoles returns the project-level IAM roles to grant the
// workstation service account based on each installed plugin's selected
// auth_provider. Skips disabled plugins and plugins with no selected
// provider. Order follows registry order; per-provider order follows
// plugin.yaml declaration order.
func pluginAuthIAMRoles(regPlugins []*plugins.FilePlugin, cfg *config.Config) []string {
	if cfg == nil || cfg.Plugins == nil {
		return nil
	}
	var roles []string
	for _, p := range regPlugins {
		pluginCfg, ok := cfg.Plugins[p.Name()]
		if !ok {
			continue
		}
		if enabled, _ := pluginCfg["enabled"].(bool); !enabled {
			continue
		}
		providerName, _ := pluginCfg["auth_provider"].(string)
		if providerName == "" {
			continue
		}
		for _, ap := range p.Schema().AuthProviders {
			if ap.Name == providerName {
				roles = append(roles, ap.IAMRoles...)
				break
			}
		}
	}
	return roles
}

// appendUniqueRoles appends each entry from extra to base only if it's not
// already present. Preserves first-occurrence order. Used so two plugins
// requesting the same role don't issue duplicate add-iam-policy-binding
// calls — the calls are idempotent at gcloud, but deduping keeps the log
// readable and the step count honest.
func appendUniqueRoles(base, extra []string) []string {
	seen := make(map[string]bool, len(base)+len(extra))
	for _, r := range base {
		seen[r] = true
	}
	for _, r := range extra {
		if seen[r] {
			continue
		}
		seen[r] = true
		base = append(base, r)
	}
	return base
}

// makeBuildDir creates a unique build context directory under
// ~/.cache/ra/build/. The base name is sanitized to its filepath.Base so
// callers cannot escape the cache root with `..` or absolute paths.
// MkdirTemp gives each invocation its own dir so concurrent `ra create`
// runs cannot clobber each other.
func makeBuildDir(home, base string) (string, error) {
	clean := filepath.Base(base)
	if clean == "" || clean == "." || clean == "/" || clean == ".." {
		clean = "ra"
	}
	parent := filepath.Join(home, ".cache", "ra", "build")
	if err := os.MkdirAll(parent, 0755); err != nil {
		return "", fmt.Errorf("creating build cache dir: %w", err)
	}
	dir, err := os.MkdirTemp(parent, clean+"-")
	if err != nil {
		return "", fmt.Errorf("creating build dir: %w", err)
	}
	return dir, nil
}
