# Architecture Decision Records

Each ADR captures one significant decision — the context, the choice, the
alternatives weighed, and the consequences — so the *why* survives after the
code changes. They are immutable once Accepted; a later ADR supersedes an
earlier one rather than editing it.

New ADRs are numbered sequentially (`NNNN-short-slug.md`) and added to the
table below.

| ADR | Title | Status | Date |
|-----|-------|--------|------|
| [0001](0001-ra-tunnel-and-plugin-port-field.md) | `ra tunnel` and the plugin `port-for-tunnel:` field | Accepted | 2026-05-15 |
| [0002](0002-claude-state-sync.md) | Per-user GCS bucket + per-workstation namespacing for `claude-sync` | Accepted | 2026-05-21 |
| [0003](0003-ssh-keepalive-iap-idle-reap.md) | SSH keepalive to survive the Cloud Workstations IAP idle reap | Accepted | 2026-05-27 |
