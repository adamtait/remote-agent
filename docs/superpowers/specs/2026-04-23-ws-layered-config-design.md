# `ws` Layered Config — Design

## Goal

Replace the current ad-hoc config merge with a layered precedence model
(CLI → project → user) where the user config is the exhaustive baseline,
hardcoded defaults are eliminated, and any missing required setting causes
a validation error at command start. Extend the config to support arbitrary
secrets and static env vars forwarded into the workstation container, with
two logical secret names (`claude_oauth`/`claude_apikey` and `github_pat`)
recognized specially so the existing credential helpers remain in place.

## Principles

1. **Layered precedence** (highest → lowest):
   1. CLI args
   2. Shared project config — `.ws/config.yaml` (committed to source control)
   3. User config — `~/.config/ws/config.yaml` (personal)
2. Every setting must come from one of those sources or `ws` throws at
   command start. No hardcoded defaults in Go.
3. User configs are **exhaustive**: every key filled, every asset
   materialized.
4. `ws setup` is the single bootstrap that produces an exhaustive user
   config and a minimal project config.

## Schema

Both files use grouped YAML. No `defaults:` wrapper, no `projects:` map.

### `~/.config/ws/config.yaml` (user — exhaustive)

```yaml
git:
  user_name: "Alice"
  user_email: "alice@example.com"

workstation:
  region: us-central1
  machine_type: e2-standard-4
  disk_size_gb: 50
  cluster_name: claude-dev-cluster
  config_name: claude-dev-config
  service_account_name: claude-workstation-sa
  image_name: claude-code-workstation
  image_tag: latest
  repo_name: claude-workstation-images
  auto_sleep_hours: 2
  auto_shutdown_hours: 12

# secrets — map GCP Secret Manager secrets to container env vars. Each
# entry has the same {secret_name, env_var} shape. ws recognizes three
# logical names and wires them into credential helpers; any other name
# is treated generically (fetched at boot, exported as env_var).
#
#   claude_oauth  → fetches at boot, exports env_var (CLAUDE_CODE_OAUTH_TOKEN).
#                   Also seeds ~/.claude.json onboarding if
#                   oauth_account_secret_name is set.
#   claude_apikey → installs Claude Code's apiKeyHelper — re-fetches every
#                   ~5 min (rotation without restart). env_var is the
#                   canonical name (ANTHROPIC_API_KEY) for documentation.
#   github_pat    → installs git's credential helper (fresh fetch per git
#                   op) AND exports env_var (GH_TOKEN) for `gh` CLI.
#                   Env var is stale on rotation; git operations stay fresh.
#
# Other logical names: plain fetch + export. Rotation requires ws stop/start.
# Exactly one of {claude_oauth, claude_apikey} must be present.
secrets:
  claude_oauth:
    secret_name: claude-auth
    env_var: CLAUDE_CODE_OAUTH_TOKEN
    # oauth_account_secret_name: claude-oauth-account   # optional
  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN

# env — plain static env vars forwarded to the container (no secret fetch).
env: {}
```

### `.ws/config.yaml` (project — required keys + overrides)

```yaml
project:
  id: my-gcp-project
  github_repo_url: https://github.com/org/repo.git

# Any group above may override per-key. Examples:
# workstation:
#   machine_type: e2-standard-8      # beefier CPU for this project
# secrets:
#   my_api_key:
#     secret_name: my-api-key
#     env_var: MY_API_KEY            # custom secret using the same template
# env:
#   MY_STATIC_VAR: foo
```

### Field reference

| Group | Key | Type | Required | Source |
|---|---|---|---|---|
| `git` | `user_name` | string | yes | user only |
| `git` | `user_email` | string | yes | user only |
| `project` | `id` | string | yes | project only |
| `project` | `github_repo_url` | string | yes | project only |
| `workstation` | `region` | string | yes | user (project override) |
| `workstation` | `machine_type` | string | yes | user (project override) |
| `workstation` | `disk_size_gb` | int | yes | user (project override) |
| `workstation` | `cluster_name` | string | yes | user (project override) |
| `workstation` | `config_name` | string | yes | user (project override) |
| `workstation` | `service_account_name` | string | yes | user (project override) |
| `workstation` | `image_name` | string | yes | user (project override) |
| `workstation` | `image_tag` | string | yes | user (project override) |
| `workstation` | `repo_name` | string | yes | user (project override) |
| `workstation` | `auto_sleep_hours` | int (nullable) | yes | user (project override) |
| `workstation` | `auto_shutdown_hours` | int | yes | user (project override) |
| `secrets[name]` | `secret_name` | string | yes per entry | either |
| `secrets[name]` | `env_var` | string | yes per entry | either |
| `secrets[name]` | `oauth_account_secret_name` | string | optional (claude_oauth only) | either |
| `env[name]` | — | string | — | either |

"User only" means the scaffold template for `.ws/config.yaml` does not
include the key, and merging never reads git identity from the project
config. "Project only" means the user config does not include the key —
there is no project-independent value.

## Precedence and merge semantics

- **Scalars** (everything in `git`, `workstation`, `project`): project
  value replaces user value if set; CLI arg replaces both.
- **`secrets` map**: merged by logical name. `project.secrets["claude_oauth"]`
  replaces `user.secrets["claude_oauth"]` entirely. Logical names present
  only in one side are kept.
- **`env` map**: merged by key name. Project value replaces user value for
  the same key; new keys accumulate.
- **CLI args**: reserved for future per-command flags that shadow config
  keys (e.g. `--region`). The load-merge-validate pipeline is structured
  to accept a third layer; today no commands define overrides.

## Validation

`internal/config/validate.go` runs after merge, called from
`cmd/root.go`'s `PersistentPreRunE` for every command except `setup`.

1. **Required keys present**: walk the field reference table; every row
   marked required must have a non-zero value. Error names the missing
   key and suggests `ws setup`.
2. **Exactly-one Claude auth**: `claude_oauth` XOR `claude_apikey` in the
   merged `secrets:` map. Error if both or neither.
3. **`github_pat` present**: required today; explicit error if missing.
4. **`env_var` names**: match `^[A-Z_][A-Z0-9_]*$`. Reject invalid shell
   identifiers before they reach `--container-env`.
5. **No env-var collisions**: no two `secrets[*].env_var` match; no
   `secrets[*].env_var` matches an `env[*]` key; neither matches a
   ws-reserved name: `GCP_PROJECT_ID`, `GITHUB_REPO_URL`, `GIT_USER_NAME`,
   `GIT_USER_EMAIL`, `WS_SECRETS`, and any `WS_SECRET_*` pattern.
6. **Asset files present**: exactly one of
   `~/.config/ws/template/<name>` or `.ws/<name>` must exist for each of
   `Dockerfile`, `entrypoint.sh`. Missing both → error.

`cmd/create.go` additionally performs **reachability validation** before
any GCP mutation: for each entry in `cfg.Secrets`, ensure the named GCP
secret exists in the target project (create-on-prompt semantics inherited
from today's `ensureClaudeSecret` / `ensureSecret`, generalized to iterate
the merged secrets list).

## Wire protocol: `ws create` → `entrypoint.sh`

`ws create` emits the container env as:

```
# Fixed, always emitted:
GCP_PROJECT_ID=<project.id>
GITHUB_REPO_URL=<project.github_repo_url>
GIT_USER_NAME=<git.user_name>
GIT_USER_EMAIL=<git.user_email>

# Secrets index + per-entry pair:
WS_SECRETS=<comma-separated logical names>
WS_SECRET_<UPPERCASED_NAME>_NAME=<secret_name>
WS_SECRET_<UPPERCASED_NAME>_VAR=<env_var>
# Optional sibling for claude_oauth:
WS_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME=<secret>

# Static env pass-through:
<key>=<value>   # one per env[key]
```

`entrypoint.sh` iterates `$WS_SECRETS`. Per logical name:

- **`claude_oauth`**: fetch via `gcloud secrets versions access --secret=$WS_SECRET_CLAUDE_OAUTH_NAME --project=$GCP_PROJECT_ID`; export under `$WS_SECRET_CLAUDE_OAUTH_VAR` in `/etc/environment` and `/etc/profile.d/claude-auth.sh`. If `$WS_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME` is set, seed `~/.claude.json` onboarding (current `seed_claude_onboarding` logic).
- **`claude_apikey`**: install `/usr/local/bin/claude-apikey-helper.sh` that reads `$WS_SECRET_CLAUDE_APIKEY_NAME` and `$GCP_PROJECT_ID` at runtime. Wire into `~/.claude/settings.json`'s `apiKeyHelper`. No env-var export.
- **`github_pat`**: install `/usr/local/bin/git-credential-gcp-secret` that reads `$WS_SECRET_GITHUB_PAT_NAME` and `$GCP_PROJECT_ID`; register it in `/etc/gitconfig`. Also fetch at boot and export `$WS_SECRET_GITHUB_PAT_VAR` for `gh` CLI convenience.
- **anything else**: fetch at boot, export as `$WS_SECRET_<NAME>_VAR`. Rotation requires container restart.

`entrypoint.sh` clears stale state for the two special env vars
(`CLAUDE_CODE_OAUTH_TOKEN`, `GH_TOKEN`) at the top of each boot before
dispatching — identical to today's cleanup block.

## `ws setup` behavior

1. Probe environment (unchanged `ProbedValues`).
2. Prompt **user** values:
   - `git.user_name`, `git.user_email` (probed default each).
   - Claude auth mode menu: `[oauth | apikey]`. Picks which logical name
     goes into `secrets:`.
   - With `--advanced`, also prompt every `workstation.*` key. Default
     (non-advanced) writes all workstation keys with their seed values.
3. Write `~/.config/ws/config.yaml` — every key filled in, no commented
   lines except documentation on `secrets:`. Idempotent without
   `--force`: skips if file exists.
4. Materialize `~/.config/ws/template/Dockerfile` and
   `~/.config/ws/template/entrypoint.sh` from the embedded seeds.
   `--force` overwrites; default skips existing files.
5. Prompt **project** values: `project.id`, `project.github_repo_url`.
6. Write `.ws/config.yaml` with the two required keys and commented-out
   override examples for each group (so users see the shape without
   un-needed overrides). `.ws/Dockerfile` and `.ws/entrypoint.sh` are
   **not** written by default.
7. With `--with-project-assets`, also copy the two asset files into
   `.ws/` so the project can override them.

Flags:

| Flag | Effect |
|---|---|
| `--force` | overwrite existing config/asset files |
| `--project-only` | skip user config + user assets |
| `--global-only` | skip project config |
| `--advanced` | prompt for every `workstation.*` key |
| `--with-project-assets` | also materialize `.ws/Dockerfile` + `.ws/entrypoint.sh` |

`ws setup template` stays a prompt-less emitter of the same schema.

## Implementation plan (file-level)

| Area | Files | Change |
|---|---|---|
| Schema | `internal/config/types.go` | Rename `GlobalConfig`→`UserConfig`. Flatten into grouped structs: `GitConfig`, `WorkstationConfig`, `ProjectSection`, `SecretEntry`. Drop `GlobalDefaults`, `GlobalProjectOverride`. Add `Secrets map[string]SecretEntry`, `Env map[string]string` on both `UserConfig` and `ProjectConfig`. |
| Load/merge | `internal/config/merge.go`, `global.go`, `merge_test.go`, `global_test.go` | Rename `LoadGlobalConfig`→`LoadUserConfig`. `Merge` takes `(user, project)` — no hardcoded defaults, no `Projects[id]` map, no three-layer fallback. Secrets/env maps merge per logical-key. |
| Required-keys validation | `internal/config/validate.go` (new), `validate_test.go` (new) | `ValidateRequired(cfg)`, `ValidateSecrets(cfg)`, composed as `Validate(cfg)`. |
| Root wiring | `cmd/root.go` | `PersistentPreRunE` adds `config.Validate(cfg)` after merge; `setup` keeps its `return nil` override. |
| Create-time validation | `cmd/create.go` | Iterate `cfg.Secrets` for GCP-reachability. Replace hardcoded `claudeAuthMode`/`claudeSecretName`/`"github-pat"` with config-driven values. Emit `WS_SECRETS` + `WS_SECRET_*_NAME`/`_VAR` + `env` keys into `--container-env`. |
| Entrypoint | `internal/scaffold/assets/entrypoint.sh`, `embedded.go` | Replace `case CLAUDE_AUTH_MODE` with a loop over `WS_SECRETS`, dispatching on logical name. Preserve helper wiring for `claude_apikey` and `github_pat`; preserve onboarding seed for `claude_oauth`. |
| Scaffold templates | `internal/scaffold/templates.go`, `templates_test.go` | New `RenderUserConfig(pv, uv)` / `RenderProjectConfig(pv, uv)` emit the grouped schema with documentation comments on `secrets:`. Delete `RenderGlobalTemplate`, `RenderProjectTemplate`, `splitCommentedDefault`, `UserProjectValues`, `UserGlobalValues`. Introduce `UserValues` / `ProjectValues` aligned with the new groups. |
| Scaffold orchestration | `internal/scaffold/scaffold.go`, `scaffold_test.go` | `InteractiveScaffold` prompt flow reflows for new groups + Claude auth menu. Add `--with-project-assets` support. Materialize user-dir asset copies in `~/.config/ws/template/`. `TemplateScaffold` emits the new schema. |
| Asset resolution | `internal/scaffold/source.go`, `source_test.go` | `ResolveSource` no longer falls back to embedded at runtime — returns error if neither user nor project asset exists. Embedded bytes are referenced only by `ws setup` as the seed. |
| Setup command | `cmd/setup.go`, `setup_test.go` | New `--with-project-assets` flag. |
| Per-command callers | `cmd/{connect,create,delete,list,logs,resolve,stop,url}.go` | Mechanical: `cfg.GitUserName` → `cfg.Git.UserName`, etc. (If we keep the flat `Config` post-merge struct, this is unnecessary — see below.) |
| Docs | `README.md`, `docs/headless-claude-auth-plan.md` | Update to the new flow and schema. |

### Post-merge `Config` shape

The parsed-YAML structs are grouped (`UserConfig.Git.UserName`). The
**merged `Config`** — consumed by every command — stays flat
(`Config.GitUserName`, `Config.Region`, `Config.WorkstationName`, etc.)
to minimize churn in `cmd/*`. `Merge` handles the lift from grouped
input to flat output. New fields added: `Config.Secrets map[string]SecretEntry`,
`Config.Env map[string]string`.

### Deletions

- `internal/config`: `GlobalDefaults`, `GlobalProjectOverride`,
  `GlobalConfig.Projects`, hardcoded defaults block in `Merge`.
- `internal/scaffold`: `UserProjectValues`, `UserGlobalValues`,
  `RenderProjectTemplate`, `RenderGlobalTemplate`, `splitCommentedDefault`.

### No migration

Per the repo's "no backwards-compat code" rule. First post-upgrade `ws`
invocation against an old config fails validation with "missing required
key …; run `ws setup`". Re-running setup regenerates a compliant config.

## Testing approach

- **Merge tests** (`merge_test.go`): rewrite to the two-layer model —
  project overrides user, nothing else. Cover secrets map merge, env map
  merge, and override collisions.
- **Validation tests** (`validate_test.go`): each required-key omission
  produces a distinct error; claude auth mutual-exclusion; env-var regex;
  collision detection; asset-file presence.
- **Scaffold tests** (`templates_test.go`): `RenderUserConfig` emits every
  required key uncommented; `RenderProjectConfig` emits the two project
  required keys uncommented and every override commented. Documentation
  comments on `secrets:` are present verbatim (asserted).
- **Interactive-setup tests** (`scaffold_test.go`): end-to-end — user
  answers prompts, expected YAML lands on disk; Claude auth menu picks
  the right logical name in `secrets:`; `--with-project-assets` copies
  assets.
- **Create-time tests** (`create_test.go`): mocked gcloud runner asserts
  the `--container-env` string includes the exact `WS_SECRETS` index and
  per-entry keys for each configured secret, plus any `env:` extras, and
  omits the old `CLAUDE_AUTH_SECRET_NAME`/`GITHUB_PAT_SECRET_NAME` keys.
- **Entrypoint tests**: none today; the change is substantial enough that
  a minimal shell-level test (invoke `entrypoint.sh` under a fake
  `gcloud` in `$PATH` emitting deterministic values, assert written
  files) is worth adding. Scope: new file
  `internal/scaffold/assets/entrypoint_test.sh` run from Go via
  `exec.Command("bash", ...)`. Optional if shelling-in-tests is out of
  scope — flag to reviewer.

## Open questions (non-blocking)

- `claude_apikey` mode today uses `ANTHROPIC_API_KEY` as the canonical
  env-var name in documentation. Do we export it at boot too (for
  non-Claude-Code tools that read `ANTHROPIC_API_KEY` directly), or keep
  apikey mode as helper-only? Current design: helper-only — the `env_var`
  field is documentation. Can be revisited.
- `claude_oauth`'s `oauth_account_secret_name` is a sibling field on the
  entry, not its own `secrets:` entry. An alternative would be a separate
  logical name (`claude_oauth_account`) with its own `{secret_name,
  env_var}`. Current design favors the sibling for locality; consider
  flipping if the generic-entry uniformity is worth more.
