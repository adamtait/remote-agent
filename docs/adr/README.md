# Architecture Decision Records

Each ADR captures one significant decision — the context, the choice, the
alternatives weighed, and the consequences — so the *why* survives after the
code changes. They are immutable once Accepted; a later ADR supersedes an
earlier one rather than editing it.

New ADRs are numbered sequentially (`NNNN-short-slug.md`) and added to the
table below.

ADRs 0004–0009 are **retrospective records**: they backfill the project's
founding architecture decisions (April 2026) from the design specs that
formerly lived under `docs/superpowers/` (recoverable from git history at
`334634a^`). They are numbered after 0001–0003 because they were written
later, but they document earlier decisions — several of which superseded one
another as the design evolved from `cws` → `ws` → `ra`. Each carries a
supersession note pointing forward.

| ADR | Title | Status | Date |
|-----|-------|--------|------|
| [0001](0001-ra-tunnel-and-plugin-port-field.md) | `ra tunnel` and the plugin `port-for-tunnel:` field | Accepted | 2026-05-15 |
| [0002](0002-claude-state-sync.md) | Per-user GCS bucket + per-workstation namespacing for `claude-sync` | Accepted | 2026-05-21 |
| [0003](0003-ssh-keepalive-iap-idle-reap.md) | SSH keepalive to survive the Cloud Workstations IAP idle reap | Accepted | 2026-05-27 |
| [0004](0004-go-cli-shelling-out-to-gcloud.md) | A Go CLI shelling out to `gcloud`, replacing the provisioning bash script | Accepted (config schema superseded by 0006) | 2026-04-21 |
| [0005](0005-embedded-overridable-build-assets.md) | Embedded, fully-overridable build assets via scaffolding | Accepted (superseded by 0008) | 2026-04-22 |
| [0006](0006-layered-config-and-secret-wire-protocol.md) | Layered config with a generic secrets/env passthrough and secret wire protocol | Accepted (special-casing superseded by 0009) | 2026-04-23 |
| [0007](0007-file-based-plugin-system.md) | File-based plugins replacing the compiled-Go plugin interface | Accepted (defaults/hooks superseded by 0008) | 2026-04-26 |
| [0008](0008-zero-builtin-plugin-image-mirror-architecture.md) | Zero built-in plugins — image-mirror layout and an ephemeral build-context assembler | Accepted | 2026-04-27 |
| [0009](0009-github-and-claude-as-external-plugins.md) | GitHub (and Claude) as external plugins; core ships plugin-agnostic | Accepted | 2026-04-27 |
| [0010](0010-ra-teardown-shared-safe-resource-removal.md) | `ra teardown` — reverse-order resource removal with a shared-vs-per-project split | Accepted | 2026-07-07 |
| [0011](0011-eternal-terminal-connect.md) | Eternal Terminal connect path (`ra connect --et`) | Accepted | 2026-06-16 |
