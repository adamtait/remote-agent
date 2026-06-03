# ADR 0009: GitHub (and Claude) as external plugins; core ships plugin-agnostic

- **Status:** Accepted (retrospective record; current — plugins now live in [`adamtait/ra-plugins`](https://github.com/adamtait/ra-plugins))
- **Date:** 2026-04-27
- **Deciders:** @adamtait

> **Retrospective note.** Backfilled from the now-removed
> `docs/superpowers/plans/2026-04-27-github-plugin-extraction.md` (git
> `334634a^`) — the one decision in this series that had only an implementation
> plan, no separate design spec. It completes the trajectory of
> [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md): with the
> mechanism for zero built-in plugins in place, the remaining GitHub-specific
> code was extracted from core. The later open-source preparation moved all
> plugins out of this repo entirely, into `adamtait/ra-plugins`.

## Context

After [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md), `ra`
core was structurally plugin-agnostic but still carried GitHub-specific logic
inherited from the original bash script: the `gh` CLI install in the core
Dockerfile, `github_pat` special-casing in the boot script and config
validation ([ADR 0006](0006-layered-config-and-secret-wire-protocol.md)),
first-boot repo cloning, the `git.github_repo_url` config field, and SSH→HTTPS
URL normalization. Core wasn't truly agnostic while it still *did GitHub things*
by default.

## Decision

**Move every GitHub-specific concern out of core into a `github` plugin**, so a
workstation only touches GitHub if the user installs it. Generic git *identity*
(`git.user_name`, `git.user_email`) stays in core; only GitHub-flavored bits
move:

```
plugins/github/
├── plugin.yaml                                    # enabled, repo_url config; pat auth provider (github_pat → GH_TOKEN)
├── Dockerfile.d/100_github-cli.sh                 # build-time gh CLI install (was in core Dockerfile)
├── workstation-startup.d/250_github-credential-helper.sh   # per-git-op PAT fetch from Secret Manager
└── workstation-startup.d/260_github-clone.sh      # first-boot git identity + clone of RA_PLUGIN_GITHUB_REPO_URL; inline SSH→HTTPS normalization
```

Repo cloning becomes the plugin's job: its first-boot script reads
`RA_PLUGIN_GITHUB_REPO_URL` from container env. Correspondingly, core deletes
`GithubRepoURL` from `GitConfig`, the `github_pat`/`github_repo_url` validation
rules, the reserved `GITHUB_REPO_URL`/`GH_TOKEN` env names, the SSH→HTTPS
helpers, and the `github_pat` dispatch case from the boot script — generalizing
[ADR 0006](0006-layered-config-and-secret-wire-protocol.md)'s special-casing
into the plugin's plain `secrets:` + `auth_providers:` declaration.

### Sequencing

Build the plugin first (additive — doesn't break the build), then strip core in
atomic commits that keep `go test ./...` green at every step. Existing tests
build fixtures programmatically, so removing the in-tree plugin code doesn't
break them.

## Alternatives considered

- **Leave GitHub in core as a "blessed" integration.** Rejected: it reintroduces
  exactly the built-in-plugin special case [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md)
  set out to eliminate. The same reasoning later applied to `claude`.
- **Move git identity into the plugin too.** Rejected: `git.user_name`/`user_email`
  are generic to any git host, not GitHub-specific, so they stay in core.

## Consequences

- `ra` core ships truly plugin-agnostic: no `gh` install, no PAT handling, no
  clone logic, no GitHub config fields.
- The `github` plugin (gh-CLI install + PAT credential helper + first-boot
  clone) holds what used to be hardcoded; installing it is what gives a
  workstation GitHub access.
- This extraction generalized to **all** integrations during open-source prep:
  `claude`, `github`, and the rest now live in the standalone
  [`adamtait/ra-plugins`](https://github.com/adamtait/ra-plugins) monorepo, and
  this repo ships zero plugins. Install one with the repo + subpath form, e.g.
  `ra plugin install https://github.com/adamtait/ra-plugins github` (see
  CLAUDE.md).
