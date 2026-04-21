# cws setup command — design

## Motivation

`cws create` currently fails fast when `.workstation.yaml` is missing in the
project tree. There is no tooling to help a new user (or a new project) get
from zero to a valid config. A `cws setup` command fills that gap: it
scaffolds both the per-project and global config files with sensible defaults
probed from the user's environment.

## Command structure

Two subcommands under a single `setup` parent:

```
cws setup              # interactive: probe + prompt + write
cws setup template     # non-interactive: write commented YAML templates
```

Shared flags on both subcommands:

| Flag              | Default  | Meaning                                              |
|-------------------|----------|------------------------------------------------------|
| `--force`         | false    | Overwrite existing target files (else: refuse)       |
| `--project-only`  | false    | Only scaffold `.workstation.yaml`                    |
| `--global-only`   | false    | Only scaffold `~/.config/cws/config.yaml`            |

`--project-only` and `--global-only` are mutually exclusive; using both is a
usage error.

Files written:
- `./.workstation.yaml`
- `~/.config/cws/config.yaml` (creating `~/.config/cws/` at `0755` if needed)

All files written at mode `0644`.

## Code layout

```
cws/
  cmd/
    setup.go          # cobra wiring for `setup` and `setup template`
    setup_test.go     # smoke tests using in-process stdin/stdout and tmp HOME/cwd
  internal/
    scaffold/
      scaffold.go     # pure file-writing + template rendering
      prompt.go       # one-field-at-a-time prompting w/ defaults + validation
      probe.go        # Prober interface + gcloud/git probes
      templates.go    # Go string constants for commented YAML templates
      *_test.go       # table-driven unit tests
```

`internal/scaffold` has no dependency on cobra so it can be exercised directly
from tests. `cmd/setup.go` only wires flags, I/O, and a `Prober` implementation.

## Interactive flow (`cws setup`)

1. **Prereq probe (warn-only).** Each check prints a single yellow
   `WARN: ...` line on failure but never blocks:
   - `gcloud --version` exits 0
   - `gcloud auth list --filter=status:ACTIVE --format=value(account)` returns
     non-empty
   - `docker --version` exits 0
   - After `project_id` is known: `gcloud beta billing projects describe <id>
     --format=value(billingEnabled)` returns `True`

2. **Probe for defaults.** All probes best-effort — missing tool or empty
   output means no default, not a failure:
   - `gcloud config get-value project` → default for `project_id`
   - `gcloud config get-value compute/region` → default for `region` (fallback
     `us-central1`)
   - `git config --get user.name` / `user.email` → defaults for git user
   - `git remote get-url origin` (if in a git repo) → default for
     `github_repo_url`

3. **Prompt** (one field at a time; `[default]` shown; Enter accepts):

   Project config:
   - **Required:** `project_id`, `github_repo_url`
   - **Advanced (skipped unless `--advanced` flag):** `region`, `cluster_name`,
     `config_name`, `machine_type`, `disk_size_gb`

   Global defaults:
   - `region`, `git_user_name`, `git_user_email`

   Validation during prompting:
   - `project_id` must match `^[a-z][-a-z0-9]{4,28}[a-z0-9]$` (GCP project-id
     regex); re-prompt on mismatch.
   - `github_repo_url` must start with `https://` or `git@`; re-prompt on
     mismatch.

4. **Confirm & write.** Print a summary of both files' contents, ask
   `Proceed? [Y/n]`. On confirm, write both files.

5. **Next-steps hint.** Print `Run 'cws create' to provision the workstation.`

The `--advanced` flag is only valid on `cws setup` (not `setup template`).

## Template mode (`cws setup template`)

Writes commented YAML templates with every field shown and documented inline.
Values probed from the environment appear as `# probed: <value>` comments so
the user can see both the placeholder and the source.

### `.workstation.yaml` template

```yaml
# cws project config — committed to the repo root
# Required fields have no default; uncomment and fill them in.
# Optional fields show the default cws will apply if left unset.

project_id: my-gcp-project        # REQUIRED — probed: <probed or 'none'>
github_repo_url: https://...      # REQUIRED — probed: <probed or 'none'>

# region: us-central1             # optional; falls back to global default, then us-central1
# cluster_name: claude-dev-cluster
# config_name:  claude-dev-config
# machine_type: e2-standard-4
# disk_size_gb: 50
```

### `~/.config/cws/config.yaml` template

```yaml
# cws global config — user-wide defaults and per-project overrides
# All fields optional; uncomment what you want to set.

defaults:
  # region: us-central1
  # git_user_name:  "<probed>"
  # git_user_email: "<probed>"

# projects:
#   my-gcp-project:
#     service_account_name: claude-workstation-sa
#     image_name:           claude-code-workstation
#     image_tag:            latest
#     repo_name:            claude-workstation-images
```

Templates are Go string constants rendered via `fmt.Sprintf` with probed
values. If a probe yielded no value, the `# probed:` comment reads
`# probed: (none — set manually)`.

## Cobra wiring and PersistentPreRunE

The existing `rootCmd.PersistentPreRunE` loads `.workstation.yaml` and returns
an error if missing. That would block `cws setup` from ever running in a fresh
directory.

Fix: define `PersistentPreRunE` on `setupCmd` that returns nil. Cobra uses
the nearest-defined `PersistentPreRunE` up the tree, so this short-circuits
the config loader. Config loading is not needed for setup.

## Error handling

- Target file exists + no `--force` → return error
  `.workstation.yaml already exists; re-run with --force to overwrite`.
  Exit code 1 (via root's error printer).
- Invalid values entered interactively → re-prompt (do not exit).
- Write failures (permissions, disk) → propagate wrapped error.
- Interactive EOF / Ctrl-D → treat as cancel, exit 1, write nothing.
- `--project-only` and `--global-only` both set → usage error.

## Atomic writes

Each target file is written by:
1. Creating a temp file in the same directory via `os.CreateTemp(dir, ".cws-scaffold-*")`.
2. Writing content + `Close()`.
3. `os.Rename` to the target path.

This ensures a partial write cannot corrupt a pre-existing file (relevant
under `--force`).

## Testing

- **`internal/scaffold` unit tests (table-driven):**
  - Template rendering: probes present, probes absent, partial probes.
  - Refuse-if-exists behavior: with and without `--force`.
  - Atomic write: temp file is removed on rename success; leftover temp files
    do not clobber the target.
  - Prompt parsing: empty input takes default, invalid input triggers
    re-prompt, Ctrl-D returns ErrCancelled.
- **`cmd/setup_test.go` smoke tests:**
  - Use `t.TempDir()` for `HOME` and `cwd`; inject stdin via a bytes buffer.
  - Assert exact file contents for both subcommands.
  - Assert `--project-only` / `--global-only` honored.
  - Assert existing-file refusal and `--force` overwrite.
- **Prober fake:** an in-memory implementation of the `Prober` interface
  returns canned values for each probe key. No real `gcloud` / `git` /
  `docker` invocations in tests.

## Out of scope

- Editing `~/.config/cws/config.yaml`'s `projects:` per-project override
  block. `cws setup` writes the `defaults:` section only; per-project
  overrides remain manual.
- Running `cws create` or any workstation provisioning. `cws setup` only
  touches local config files.
- Secret scaffolding (Claude token, GitHub PAT). Those are handled by
  `cws create` at first run.
- Editing existing files in-place. Merge-mode scaffolding was rejected in
  favor of the simpler `--force` overwrite.
