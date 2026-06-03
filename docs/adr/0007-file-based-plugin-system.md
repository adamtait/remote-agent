# ADR 0007: File-based plugins replacing the compiled-Go plugin interface

- **Status:** Accepted (retrospective record; the file-based format is current, the embedded-defaults + `install.sh`/`setup.sh` shape was **superseded** by [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md))
- **Date:** 2026-04-26
- **Deciders:** @adamtait

> **Retrospective note.** Backfilled from the now-removed
> `docs/superpowers/specs/2026-04-26-plugin-system-design.md` (git `334634a^`).
> This ADR records the move from Go-compiled plugins to a `plugin.yaml`-driven
> file format — the format `ra` still uses. The *specifics* introduced here
> (a `claude` plugin shipped as embedded Go defaults; `install.sh` + `setup.sh`
> as the two script hooks; discovery only under `plugins/<name>/`) were replaced
> one day later by [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md).

## Context

Integrations like Claude Code were compiled into the binary behind a Go
`Plugin` interface (`internal/plugins/`). Adding or customizing one required
writing Go and rebuilding `ra`. We wanted anyone — without Go knowledge — to
author a plugin, modeled on the Claude Code plugin system: a directory of
simple declarative + shell files.

## Decision

Replace the compiled `Plugin` interface with a **file-based plugin format**. A
plugin is a directory:

```
<plugin-dir>/<name>/
├── plugin.yaml     # required — metadata, config fields, secrets, auth providers
├── install.sh      # optional — runs at Docker image build time (root, no secrets)
└── setup.sh        # optional — sourced by the boot entrypoint each container start
```

### `plugin.yaml` schema

Declares `name`/`version`/`description`, a `config:` list (typed fields with
`default`/`required`/`options` that drive `ra setup` prompts and validation),
top-level `secrets:` (always fetched), and optional `auth_providers:` (each with
its own additional `secrets:`, fetched only when that provider is active). Plugin
config is namespaced in the config YAML under `plugins.<name>.<field>`; the
typed `Claude *ClaudeConfig` field is replaced by a generic
`Plugins map[string]map[string]interface{}`.

### Discovery and shadowing

Plugins load from two locations: `~/.config/ra/plugins/<name>/` (user-global)
then `.ra/plugins/<name>/` (project-local). **A project-local plugin shadows a
user-global one of the same name entirely** — no merging. At startup `ra` scans
both and loads every directory containing a `plugin.yaml`.

### Runtime env contract

A plugin's `setup.sh` receives its secrets under their declared `env_var`
names, its config under `RA_PLUGIN_<NAME>_<FIELD>` (uppercased), and standard
workstation vars (`RA_PROJECT_ID`, `RA_WORKSTATION_NAME`). The entrypoint
glob-sources each active plugin's `setup.sh` in load order (user-global first,
project-local last), after the generic secret-dispatch loop from
[ADR 0006](0006-layered-config-and-secret-wire-protocol.md).

### Go internals

`internal/plugins/fileplugin.go` (`FilePlugin` reads a directory and implements
every plugin behavior), `loader.go` (`LoadAll()` scans both locations + shadows
by name), and a simplified `registry.go` wrapping `[]*FilePlugin` with an
**unchanged aggregate API** (`AllAssets`, `ValidateAll`,
`AllContainerEnvPairs`) so `cmd/*` callers don't change. The Go `Plugin`
interface and the compiled `claude` package are deleted; `claude` ships as
embedded default files instead.

## Alternatives considered

- **Keep compiled-Go plugins.** Rejected: authoring requires Go + a rebuild,
  excluding non-Go users and AI agents editing files directly.
- **A single monolithic plugin script** rather than `plugin.yaml` + scripts.
  Rejected: the declarative `plugin.yaml` is what lets `ra setup`/`ra validate`
  prompt and check generically without executing anything.
- **Merge project + user plugins of the same name.** Rejected: ambiguous
  precedence; whole-plugin shadowing is predictable.

## Consequences

- Plugins become writable by anyone as data + shell, and configurable through
  the generic `plugins.<name>.*` namespace.
- The registry's stable aggregate API kept the blast radius inside
  `internal/plugins` — `cmd/*`, `internal/scaffold`, and `internal/gcloud` were
  untouched.
- **Superseded by [ADR 0008](0008-zero-builtin-plugin-image-mirror-architecture.md)
  one day later:** embedded in-binary plugin defaults were removed (zero
  built-in plugins), the `install.sh`/`setup.sh` pair was replaced by numbered
  drop-ins under three image-mirror directories, and `ra plugin install` took
  over config/secret prompting. The durable decision from this ADR is the
  `plugin.yaml`-driven, file-based format itself.
