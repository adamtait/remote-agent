# ADR 0008: Zero built-in plugins — image-mirror layout and an ephemeral build-context assembler

- **Status:** Accepted (retrospective record; this is the current architecture, described in CLAUDE.md)
- **Date:** 2026-04-27
- **Deciders:** @adamtait

> **Retrospective note.** Backfilled from the now-removed
> `docs/superpowers/specs/2026-04-27-generic-plugin-system-design.md` (git
> `334634a^`). Unlike the earlier ADRs in this series, the decision here is
> **still in force** — it is the architecture CLAUDE.md documents. It supersedes
> the `.ws/`-asset model of [ADR 0005](0005-embedded-overridable-build-assets.md)
> and the embedded-defaults / `install.sh`+`setup.sh` aspects of
> [ADR 0007](0007-file-based-plugin-system.md).

## Context

[ADR 0007](0007-file-based-plugin-system.md) made plugins file-based but still
shipped a `claude` plugin embedded in the binary, and the on-disk layout was a
patchwork: a `template/` dir for user assets ([ADR 0005](0005-embedded-overridable-build-assets.md)),
a `.ws/entrypoint.sh`, a staged `profile.d`, and `install.sh`/`setup.sh` hooks
whose relationship to the final image was implicit. We wanted `ra` core to be
**truly plugin-agnostic** — zero built-in plugins, not one claude-shaped
exception — and we wanted the path from "files on disk" to "files in the image"
to be obvious rather than translated.

## Decision

### Ship zero built-in plugins

`ra setup` configures only `ra` itself — no plugin prompting, no plugin asset
materialization. **`ra plugin install <git-url-or-path>` becomes the only way
to add a plugin**: it clones (or copies a local path), prompts for config +
auth provider + secret *values*, uploads the values to GCP Secret Manager, and
writes the `plugins.<name>.*` / `secrets.*` blocks plus a `.ra-install.yaml`
metadata file. The in-tree `claude` plugin is moved to a top-level `plugins/`
dir as a staging post for extraction to its own repo (see
[ADR 0009](0009-github-and-claude-as-external-plugins.md)).

### Everything mirrors the image's `/etc/` layout

User-global (`~/.config/ra/`), project-local (`.ra/`), and a plugin's own
directory all mirror where files land in the image:

```
{~/.config/ra,.ra}/                 <plugin>/<name>/
├── config.yaml                     ├── plugin.yaml
├── Dockerfile                      ├── Dockerfile.d/<NNN>_*.sh
├── Dockerfile.d/                   ├── workstation-startup.d/<NNN>_*.sh
├── workstation-startup.d/          └── profile.d/<NNN>_*.sh
├── profile.d/
└── plugins/<name>/...
```

`install.sh`/`setup.sh` are gone. Every plugin script is a **numbered drop-in**
to one of three image-mirror directories: `Dockerfile.d/` (build-time hooks),
`workstation-startup.d/` (runtime startup, run independently after core's
`200_` script), and `profile.d/` (login-shell). First-boot install is just a
`workstation-startup.d/` script with an inline marker check.

### Ephemeral build-context assembler

`ra create` assembles a fresh build context each run (today at
`~/.cache/ra/build/<name>/`, never under the project dir) and deletes it on
exit. Resolution rules:

- **Single file** (`Dockerfile`): project → user → embedded (first match wins).
- **Directory merge** (`Dockerfile.d/`, `workstation-startup.d/`, `profile.d/`):
  plugin files, then user-global, then project-local; same-name files shadow;
  sorted by filename for deterministic build order.
- **Whole-plugin staging** (`plugins/<name>/`): project shadows user; the
  `.ra-install.yaml` metadata file is skipped so it never lands in the image.

Plugins are staged twice — into `plugins/<name>/` (informational, copied to
`/etc/ra/plugins/`) and into the merged top-level dirs where they actually run.
Empty stage dirs get a `.ra-keep` placeholder so the Dockerfile's `COPY dir/`
never fails.

### Generic Dockerfile

The embedded `Dockerfile` references build-context-relative paths only (no
`.ra/` prefix) and contains no integration-specific lines — it installs core
system packages, then runs every `Dockerfile.d/*.sh` hook and copies the three
merged directories into place. (The `gh`-CLI install seen here later moved out
under [ADR 0009](0009-github-and-claude-as-external-plugins.md).)

## Alternatives considered

- **Keep one embedded `claude` default** for a batteries-included experience.
  Rejected: a single built-in plugin is still a special case in "generic" core;
  zero is the only number that keeps core honest. Plugins are an external
  concern (CLAUDE.md).
- **A persistent build directory.** Rejected: an ephemeral context (removed
  every run, success or failure) keeps `ra create` runnable from any cwd and
  leaves no stale state.
- **Translate `install.sh`/`setup.sh` into image steps at build time.**
  Rejected in favor of numbered drop-ins whose destination *is* their directory
  — no translation layer to reason about.

## Consequences

- `ra` core has no integration-specific code; a workstation does something only
  if the user installs a plugin for it.
- The on-disk → in-image mapping is one-to-one, so plugin authors place a file
  where it should land in `/etc/`.
- The assembler, image-mirror layout, ephemeral build context, and
  `ra plugin install` lifecycle described here are the system documented in
  CLAUDE.md today.
- This decision set up the final extraction step
  ([ADR 0009](0009-github-and-claude-as-external-plugins.md)), after which the
  repo shipped no plugins at all.
