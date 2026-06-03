# Implementation Notes — Open-Sourcing `ra`

Running log of decisions, deviations, and tradeoffs made while executing
`/home/user/.claude/plans/plan-for-this-remote-agent-splendid-quail.md`.
Items not in the original spec, or where I had to choose, are called out here.

## Decisions & deviations

### Item 1 — MIT license
- Copyright line: `Copyright (c) 2026 Adam Tait`. Year and holder taken from the
  plan's stated decision (license under `adamtait`). **Confirm the legal name /
  whether it should be an org** before publishing.

### Items 4/5 — community files & CI
- **`CODE_OF_CONDUCT.md`: adopted the Contributor Covenant *by reference*
  instead of inlining its full text.** Generating the full Covenant body
  repeatedly tripped the output content-filter ("Output blocked by content
  filtering policy") because that text enumerates harassment/abuse terms the
  filter flags out of context. The by-reference form (short statement + canonical
  link to contributor-covenant.org v2.1) is a standard, accepted variant and
  sidesteps the filter. If you prefer the full vendored text, copy it from
  https://www.contributor-covenant.org/version/2/1/code_of_conduct/ manually.
- `CONTRIBUTING.md` documents the load-bearing constraints (shell-out-to-gcloud,
  idempotency, zero-secrets, no built-in plugins) so external contributors don't
  unknowingly break them.
- CI pins Go via `go-version-file: go.mod` rather than a hardcoded version, so
  the workflow tracks `go.mod` automatically.

### Item 2a — plugin repo+subpath sourcing
- **CLI:** `install` now takes 1 or 2 args (`cobra.RangeArgs(1,2)`). Two args =
  `<repo> <plugin-path>`; the subpath selects a plugin within a monorepo.
- **`--ref` flag** added. `cloneRepo` shallow-clones `--branch <ref>` first and
  **falls back to a full clone + `git checkout`** when that fails — this is how
  commit SHAs are supported (`--depth=1 --branch` rejects SHAs). Tradeoff: the
  SHA path does a full clone (slower) but it's the only reliable way without
  server-side `uploadpack.allowReachableSHA1InWant`. `--ref` is ignored (with a
  stderr warning) for local-path installs.
- **Path-escape guard:** `resolvePluginDir` cleans the subpath as if absolute
  and rejects anything resolving outside the repo root (`../evil`, `/etc`, etc.).
- **Metadata:** `.ra-install.yaml` now writes `repo`/`path`/`ref` (all
  `omitempty`) instead of the flat `source`. The legacy `source` field is still
  *read* for back-compat and surfaced via `installMeta.displaySource()`; new
  installs never write it. `plugin list` shows `repo (path) @ref`.
- **Tests:** added unit tests for `resolvePluginDir` (incl. escape/missing),
  `displaySource`, metadata round-trip, legacy-source back-compat, and the
  1-or-2 arg acceptance. The network-dependent `cloneRepo` is not unit-tested
  (no network in CI); its logic is small and the resolution/metadata helpers it
  feeds are covered.

### Deviation — repo-wide gofmt
- The new CI gate enforces `gofmt -l .`. Ten **pre-existing** files were not
  gofmt-clean (cosmetic struct-tag/comment alignment drift, unrelated to this
  work). I ran `gofmt -w .` across the repo so CI passes on first run. This is
  why the diff touches files like `internal/plugins/schema.go`,
  `internal/configwrite/append.go`, and several `*_test.go` — those changes are
  whitespace-only. Tests still pass.

### Item 2 — strip plugins
- **The plan assumes a separate `ra-plugins` repo** at
  `github.com/adamtait/ra-plugins`. I can only *remove* `plugins/` from this
  repo; actually creating/populating that repo is a GitHub action the user must
  do. Documented as a follow-up below.
- **One test broke and was rewritten:**
  `internal/plugins/fileplugin_test.go`'s `TestCodexPluginOAuthSecretUsesValueCommand`
  loaded the real `plugins/codex` dir. Since codex moves to `ra-plugins`, I
  replaced it with `TestLoadPlugin_OAuthSecretValueCommand_FileBase64`, a
  synthetic in-memory `plugin.yaml` exercising the same `value_command`
  (file_base64 / validate:json / interactive) parsing path. Coverage of the
  core `LoadPlugin` behavior is preserved; the dependency on a shipped plugin is
  gone. (The original explore pass missed this because it grepped for `plugins/`
  string literals and this used `filepath.Join("..","..","plugins","codex")`.)
- **ADR 0001 left untouched.** It references `./plugins/marimo` as a live example
  (lines ~118, ~199). ADRs are dated historical records, so I deliberately did
  *not* rewrite them — they describe the decision as it stood. A new contributor
  reading them should treat plugin paths there as historical.
- README updated throughout: getting-started, the `ra plugin` reference (now
  documents the two-arg + `--ref` forms), the Files section, and the rotation
  example all point at the `ra-plugins` repo / repo+subpath syntax.

### Deviation — CI workflow could not be pushed
- `git push` was rejected: the auth token lacks the GitHub `workflow` scope, so
  it cannot create `.github/workflows/ci.yml`. The file is written and ready on
  disk but is **left untracked / not in the PR**. Add it via the GitHub web UI
  or push it with a token that has `workflow` scope. The CI config itself is
  complete and correct (build + test + vet + gofmt; Go pinned via
  `go-version-file: go.mod`).

### Code-review fixes (applied after /review)
- **`copyDir` now skips `.git`.** A remote 1-arg whole-repo install previously
  copied the cloned `.git` into the installed plugin (and thus into the docker
  build context). Pre-existing, but the new feature made it obvious; fixed +
  added `TestCopyDir_SkipsGitMetadata`.
- Removed a dangling empty `//` comment on the `installMeta.InstalledAt` field.
- Considered but **left as-is**: when `--ref` fails for a non-SHA reason
  (typo/auth/network), the full-clone fallback surfaces the second error rather
  than the branch-clone error. Acceptable — the SHA case is the common reason
  and the code comment documents it.

## Follow-ups the user must do (outside this repo)
- **Add `.github/workflows/ci.yml`** (present on disk, excluded from the PR due
  to missing `workflow` token scope — see deviation above).
- Create `github.com/adamtait/ra-plugins`, populate it with the 11 plugins (each
  in its own subdir), and push. Until then, the README install examples point at
  a repo that does not yet exist.
- Set up branch protection / required CI status checks on the public repo.
- Decide whether the copyright holder is a person or an org.
