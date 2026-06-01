# Contributing to `ra`

Thanks for your interest in improving `ra`. This document covers how to build,
test, and submit changes.

## Development setup

You need a recent Go toolchain (see the `go` directive in [`go.mod`](go.mod)).
GCP access is **not** required to build or run the test suite — the tests that
shell out to `gcloud` skip themselves when it is not on `PATH`.

```bash
make build    # compile → ./ra
make install  # install to ~/.local/bin/ra
make test     # go test ./...
make clean    # remove ./ra
```

Run a single package's tests:

```bash
go test ./internal/config/... -run TestMerge
```

Before opening a PR, make sure the following are clean (this mirrors CI):

```bash
go build ./...
go vet ./...
gofmt -l .        # should print nothing
go test ./...
```

## Project conventions

A few constraints are load-bearing — please preserve them:

- **All GCP operations shell out to the `gcloud` binary.** There is no GCP Go
  SDK dependency, by design. Keep it that way.
- **Provisioning steps are idempotent** (skip-if-exists) except workstation
  creation itself.
- **Zero secrets in config.** Credentials live only in GCP Secret Manager;
  config carries logical names and env-var names, never values. Never commit a
  real secret, project ID, or personal identifier.
- `ra` ships with **no built-in plugins**. Plugins (Claude Code, GitHub, etc.)
  live in a separate repository and are added with `ra plugin install`.

See [CLAUDE.md](CLAUDE.md) for a fuller tour of the architecture and
[docs/adr/](docs/adr/) for the reasoning behind key design decisions.

## Pull requests

1. Branch off `main`.
2. Keep changes focused; add or update tests for behavior changes.
3. Ensure the checks above pass.
4. Describe the change and its motivation in the PR body. If it alters an
   architectural decision, consider adding or updating an ADR.

## Reporting bugs and requesting features

Use the GitHub issue templates. For security issues, **do not** open a public
issue — follow [SECURITY.md](SECURITY.md).
