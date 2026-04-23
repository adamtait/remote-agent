# Drop `GH_TOKEN` Env Var Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop writing the GitHub PAT to `/etc/environment` as `GH_TOKEN`. Instead install a `/usr/local/bin/gh` wrapper that fetches the PAT from Secret Manager on each invocation and exec's the real `/usr/bin/gh`.

**Architecture:** Mirrors the existing `git-credential-gcp-secret` helper pattern. `entrypoint.sh` heredocs a wrapper script, `chmod 0755`, sanity-checks it with `gh auth status`. PATH precedence (`/usr/local/bin` before `/usr/bin`) means every `gh` invocation goes through the wrapper. The PAT never persists on disk; rotations propagate on the next invocation.

**Tech Stack:** Bash (entrypoint.sh + wrapper), gcloud Secret Manager, gh CLI.

---

## File Structure

### Modified files

| File | What changes |
|---|---|
| `.ws/entrypoint.sh` | Remove the `sed -i '/^GH_TOKEN=/d'` line from the Claude pre-cleanup block (line 137). Replace the GH_TOKEN-fetch-and-write block (lines 283–297) with a block that heredocs `/usr/local/bin/gh`, `chmod 0755`, and runs `gh auth status` as a sanity check (logging a WARNING on failure). Keep the one-time `sed -i '/^GH_TOKEN=/d' /etc/environment` cleanup, relocated into the new wrapper-install block where it's contextually clear. |
| `README.md` | Top-of-file v2.1 list: drop the implicit GH_TOKEN reference (none currently — verify). "Files" section: no change. "How auth actually works at runtime" section: add a **GitHub `gh` CLI** subsection parallel to the existing **GitHub PAT** subsection. "Security notes": replace the "GitHub PAT is fetched per-operation" sentence with one that also covers the `gh` wrapper. |

### No new Go code

This change is shell + docs only. There are no unit tests to write — verification is by `bash -n` syntax check, shellcheck (if available), and end-to-end testing on a real workstation rebuild.

---

## Task 1: Replace the GH_TOKEN-write block with the `gh` wrapper install

**Files:**
- Modify: `.ws/entrypoint.sh:283-297`

- [ ] **Step 1: Verify current state of the block to be replaced**

Run: `sed -n '283,297p' .ws/entrypoint.sh`

Expected output:
```
# Export GH_TOKEN so the gh CLI authenticates without a separate `gh auth login`.
# Uses the same PAT already stored in Secret Manager.
log "Fetching GH_TOKEN from Secret Manager..."
GH_PAT=$(gcloud secrets versions access latest \
    --secret="${GITHUB_PAT_SECRET_NAME}" \
    --project="${GCP_PROJECT_ID}" 2>/dev/null || true)

sed -i '/^GH_TOKEN=/d' /etc/environment
if [[ -n "${GH_PAT}" ]]; then
    echo "GH_TOKEN=${GH_PAT}" >> /etc/environment
    log "GH_TOKEN written to /etc/environment."
else
    log "WARNING: GH_TOKEN fetch failed. Check SA permissions."
fi
unset GH_PAT
```

If the line numbers don't match, search for `GH_TOKEN written to /etc/environment` and adjust.

- [ ] **Step 2: Replace the block with the wrapper install**

Use the Edit tool to replace the block above with:

```bash
# =============================================================================
# gh CLI wrapper — fetches the PAT per-invocation from Secret Manager and
# forwards to the real /usr/bin/gh. PATH precedence (/usr/local/bin before
# /usr/bin) means every `gh` call goes through this wrapper. The PAT never
# persists on disk; rotations are picked up on the next invocation.
#
# Replaces the prior GH_TOKEN-in-/etc/environment approach, which lost a
# boot-order race against code-oss (terminals spawned from the IDE never
# inherited the env var) and exposed the PAT in a 0644 file.
# =============================================================================
GH_WRAPPER_PATH="/usr/local/bin/gh"
log "Installing gh wrapper → ${GH_WRAPPER_PATH}"

# One-time cleanup: remove any GH_TOKEN= line written by a prior install.
sed -i '/^GH_TOKEN=/d' /etc/environment

cat > "${GH_WRAPPER_PATH}" <<EOF
#!/bin/bash
# Auto-generated at workstation boot. Do not edit.
# Fetches the current GitHub PAT from Secret Manager and forwards to the
# real gh CLI. Token never persists; rotations are picked up on the next
# invocation.
GH_TOKEN=\$(gcloud secrets versions access latest \\
    --secret='${GITHUB_PAT_SECRET_NAME}' \\
    --project='${GCP_PROJECT_ID}' 2>/dev/null) || true
exec env GH_TOKEN="\${GH_TOKEN}" /usr/bin/gh "\$@"
EOF
chmod 0755 "${GH_WRAPPER_PATH}"

# Sanity check: the wrapper should authenticate against github.com.
if ! "${GH_WRAPPER_PATH}" auth status >/dev/null 2>&1; then
    log "WARNING: gh wrapper failed auth status. Check SA permissions on ${GITHUB_PAT_SECRET_NAME}."
fi
```

Notes for the editor:
- Inside the heredoc the `$(...)`, `\\` line-continuation, `\${...}` and `\$@` are escaped so they end up literal in the wrapper file. `${GITHUB_PAT_SECRET_NAME}` and `${GCP_PROJECT_ID}` are unescaped on purpose: they're substituted at install time, exactly like the git-credential-gcp-secret block at lines 247–274.
- The `EOF` is unquoted — same as the existing helpers above it — so the install-time substitutions happen.

- [ ] **Step 3: Verify shell syntax**

Run: `bash -n .ws/entrypoint.sh`

Expected: exit 0, no output. Any syntax error here means the heredoc escaping is wrong — re-check `\\`, `\$`, and `\${...}` against the existing `git-credential-gcp-secret` block (lines 247–274) which uses identical escaping.

- [ ] **Step 4: Verify the rendered wrapper would be sane**

Run:
```bash
GITHUB_PAT_SECRET_NAME=test-secret GCP_PROJECT_ID=test-project \
  bash -c 'cat <<EOF
#!/bin/bash
GH_TOKEN=\$(gcloud secrets versions access latest \\
    --secret='"'"'${GITHUB_PAT_SECRET_NAME}'"'"' \\
    --project='"'"'${GCP_PROJECT_ID}'"'"' 2>/dev/null) || true
exec env GH_TOKEN="\${GH_TOKEN}" /usr/bin/gh "\$@"
EOF'
```

Expected: a wrapper with literal `$()`, `${GH_TOKEN}`, `$@`, and the substituted secret name + project ID. (This step is purely a sanity check on the heredoc escaping you just wrote — skip if Step 3 passes and you're confident.)

- [ ] **Step 5: Commit**

```bash
git add .ws/entrypoint.sh
git commit -m "$(cat <<'EOF'
feat(entrypoint): install /usr/local/bin/gh wrapper, drop GH_TOKEN env var

Replaces the GH_TOKEN-to-/etc/environment write with a wrapper script
that fetches the PAT from Secret Manager on each invocation and exec's
the real /usr/bin/gh. Mirrors the existing git-credential-gcp-secret
pattern.

Fixes two problems with the prior approach:
  1. Boot-order race: code-oss starts before entrypoint finishes, so
     terminals spawned from the IDE never inherited GH_TOKEN.
  2. Plaintext PAT in /etc/environment (0644).

The token now never persists on disk and PAT rotations propagate on
the next gh invocation with no restart.
EOF
)"
```

---

## Task 2: Remove the redundant `sed` cleanup from the Claude pre-cleanup block

**Files:**
- Modify: `.ws/entrypoint.sh:137`

The `sed -i '/^GH_TOKEN=/d' /etc/environment` line at the top of the file (inside the Claude auth pre-cleanup) was deleting the env var line so the OAuth-mode block could re-write it. With Task 1 the rewrite is gone, and the equivalent cleanup now lives inside the new wrapper-install block, where it's contextually clear. The line at 137 is dead code — remove it.

- [ ] **Step 1: Verify the line still exists**

Run: `grep -n "^sed -i '/\\^GH_TOKEN=/d' /etc/environment\$" .ws/entrypoint.sh`

Expected: one match at line 137 (top of file, in the Claude pre-cleanup block — NOT the one you added inside the new wrapper-install block in Task 1).

If you see two matches, the second is the one you intentionally added in Task 1 — leave it. Remove only the one near line 137.

- [ ] **Step 2: Remove the line**

Use the Edit tool. The unique context around the line to remove:

old_string:
```
rm -f "${OAUTH_PROFILE_PATH}"
rm -f "${APIKEY_HELPER_PATH}"
sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
sed -i '/^GH_TOKEN=/d' /etc/environment
```

new_string:
```
rm -f "${OAUTH_PROFILE_PATH}"
rm -f "${APIKEY_HELPER_PATH}"
sed -i '/^CLAUDE_CODE_OAUTH_TOKEN=/d' /etc/environment
```

- [ ] **Step 3: Verify shell syntax**

Run: `bash -n .ws/entrypoint.sh`

Expected: exit 0.

- [ ] **Step 4: Verify exactly one `GH_TOKEN=` sed remains**

Run: `grep -c "GH_TOKEN=/d" .ws/entrypoint.sh`

Expected: `1` — the one inside the new wrapper-install block from Task 1. If you get `0`, you removed the wrong one; revert. If you get `2`, the duplicate from before Task 2 is still present.

(The pattern `GH_TOKEN=/d` is the unique tail of the sed expression `/^GH_TOKEN=/d` — anchoring with `^` in grep would fail because the file line starts with `sed`, not `^GH_TOKEN`.)

- [ ] **Step 5: Commit**

```bash
git add .ws/entrypoint.sh
git commit -m "$(cat <<'EOF'
refactor(entrypoint): drop redundant GH_TOKEN sed from Claude pre-cleanup

The cleanup line in the Claude pre-cleanup block existed to wipe
GH_TOKEN= before the OAuth-mode block re-wrote it. With the wrapper
install in the previous commit, there's no rewrite, and the equivalent
cleanup now lives inside the wrapper-install block where it reads
contextually.
EOF
)"
```

---

## Task 3: Update README — add `gh` CLI subsection, drop env-var references

**Files:**
- Modify: `README.md`

Two edits:

1. **"How auth actually works at runtime"** — add a **GitHub `gh` CLI** subsection right after the **GitHub PAT** paragraph (current lines 178–185).
2. **"Security notes"** — extend the GitHub PAT sentence to also mention the `gh` wrapper.

Also: the v2.1 top-of-file list mentions the apiKeyHelper, OAuth, and GitHub PAT credential helper. The `gh` wrapper is the same pattern; add a one-liner to the list so it stays a complete inventory.

- [ ] **Step 1: Add the `gh` wrapper to the v2.1 top-of-file list**

Use the Edit tool. Insert a new bullet after the **GitHub PAT** bullet (currently lines 20–22):

old_string:
```
- **GitHub PAT** uses a custom `credential.helper` script that calls
  `gcloud secrets versions access` on every git operation. Rotations are
  picked up immediately on the next push/pull/clone, no restart.

The `--container-env` block now contains only pointers
```

new_string:
```
- **GitHub PAT** uses a custom `credential.helper` script that calls
  `gcloud secrets versions access` on every git operation. Rotations are
  picked up immediately on the next push/pull/clone, no restart.
- **GitHub `gh` CLI** uses a wrapper at `/usr/local/bin/gh` (shadows the
  real `/usr/bin/gh` via PATH precedence) that fetches the same PAT from
  Secret Manager per invocation and forwards via `GH_TOKEN`. No env var
  on the workstation; rotations propagate on the next `gh` call.

The `--container-env` block now contains only pointers
```

- [ ] **Step 2: Add the **GitHub `gh` CLI** subsection in "How auth actually works at runtime"**

Use the Edit tool. Insert after the GitHub PAT paragraph (currently ends at line 185).

old_string:
```
**GitHub PAT.** At every container boot, `entrypoint.sh` writes
`/usr/local/bin/git-credential-gcp-secret`, a script implementing git's
credential helper protocol. On each git operation against `github.com`,
git invokes the helper, which fetches the current PAT from Secret Manager
and prints `username=oauth2\npassword=<token>` to stdout. The helper
ignores other hosts and never persists credentials. `~/.gitconfig` (on
the persistent home disk) points at this helper via
`credential.helper = /usr/local/bin/git-credential-gcp-secret`.

## Cost notes
```

new_string:
```
**GitHub PAT.** At every container boot, `entrypoint.sh` writes
`/usr/local/bin/git-credential-gcp-secret`, a script implementing git's
credential helper protocol. On each git operation against `github.com`,
git invokes the helper, which fetches the current PAT from Secret Manager
and prints `username=oauth2\npassword=<token>` to stdout. The helper
ignores other hosts and never persists credentials. `~/.gitconfig` (on
the persistent home disk) points at this helper via
`credential.helper = /usr/local/bin/git-credential-gcp-secret`.

**GitHub `gh` CLI.** At every container boot, `entrypoint.sh` writes
`/usr/local/bin/gh`, a wrapper that fetches the current PAT from Secret
Manager and exec's the real `/usr/bin/gh` with `GH_TOKEN` set in the
child process only. Because `/usr/local/bin` is ahead of `/usr/bin` on
`PATH`, every `gh` invocation — from a terminal, from Claude Code, from
a script — goes through the wrapper. The token never persists in env
or on disk, and rotating the PAT secret propagates on the next `gh`
call. A boot-time `gh auth status` smoke test logs a WARNING if the
service account can't reach the secret.

## Cost notes
```

- [ ] **Step 3: Extend the "Security notes" GitHub PAT sentence**

Use the Edit tool.

old_string:
```
In API-key mode, the Claude key is fetched into memory by `apiKeyHelper`
on demand and never touches disk. In OAuth mode, the token is written to
`/etc/profile.d/claude-auth.sh` (mode 0644) because exporting a value into
login shells requires a file the shell can read; this is equivalent in
exposure to an env var. The GitHub PAT is fetched per-operation by the git
credential helper and never persisted.
```

new_string:
```
In API-key mode, the Claude key is fetched into memory by `apiKeyHelper`
on demand and never touches disk. In OAuth mode, the token is written to
`/etc/profile.d/claude-auth.sh` (mode 0644) because exporting a value into
login shells requires a file the shell can read; this is equivalent in
exposure to an env var. The GitHub PAT is fetched per-operation — by the
git credential helper for git operations, and by the `/usr/local/bin/gh`
wrapper for `gh` CLI calls — and is never persisted to env or disk.
```

- [ ] **Step 4: Verify no stale `GH_TOKEN` references remain**

Run: `grep -n GH_TOKEN README.md`

Expected: no matches. If any appear, they're from prior wording you missed — remove them.

- [ ] **Step 5: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(readme): document /usr/local/bin/gh wrapper, drop GH_TOKEN refs

Adds the gh wrapper to the v2.1 credential-handling inventory, gives
it a parallel "How auth actually works at runtime" subsection alongside
git PAT and Claude OAuth, and updates Security notes to reflect that
both git operations and gh calls now fetch the PAT per-invocation with
no env var or disk persistence.
EOF
)"
```

---

## Task 4: End-to-end verification on a rebuilt workstation

This task can only run after the workstation is rebuilt against the new image. The previous tasks change the source; this task confirms the deployed behavior matches the spec's test plan.

- [ ] **Step 1: Push the branch and rebuild the workstation**

```bash
git push -u origin feat/drop-gh-token-env-var
ws stop
ws create   # rebuilds the image with the updated entrypoint
```

Wait for `ws create` to finish (~1–3 minutes once the cluster exists).

- [ ] **Step 2: Open a terminal in the IDE and verify the wrapper is in place**

In the workstation IDE terminal:

```bash
which gh
```

Expected: `/usr/local/bin/gh`. If it returns `/usr/bin/gh`, the wrapper wasn't installed — check `/var/log/claude-setup.log` for the entrypoint output.

- [ ] **Step 3: Verify GH_TOKEN is unset everywhere**

```bash
printenv GH_TOKEN; echo "exit=$?"
grep ^GH_TOKEN= /etc/environment; echo "exit=$?"
```

Expected:
- `printenv` exits with `1` (env var not present) and prints nothing.
- `grep` exits with `1` (no match) and prints nothing.

If either still shows a value, the entrypoint hasn't run with the new code — check the boot log.

- [ ] **Step 4: Verify `gh` authenticates transparently**

```bash
gh auth status
gh pr list --limit 3
```

Expected: `gh auth status` reports the PAT as the active credential (token source: `GH_TOKEN`). `gh pr list` succeeds without ever prompting for `gh auth login`.

- [ ] **Step 5: Verify rotation propagates without restart**

(Optional — skip unless explicitly testing rotation.)

```bash
# From a machine with secretmanager.admin on the project:
echo -n 'ghp_NEWVALUE' | gcloud secrets versions add github-pat \
    --data-file=- --project="${GCP_PROJECT_ID}"

# Then in the workstation terminal:
gh pr list --limit 1
```

Expected: succeeds, picking up the new PAT with no `ws stop`/`ws create` and no terminal restart. If the new token is invalid, `gh` will report an auth failure — that's expected behavior, just rotate again.

- [ ] **Step 6: Verify the boot-time sanity check actually fires**

```bash
grep -E '(gh wrapper|Installing gh wrapper)' /var/log/claude-setup.log
```

Expected: at least one `Installing gh wrapper → /usr/local/bin/gh` line per boot. If the workstation's SA is missing `secretmanager.secretAccessor` on the PAT secret, you should also see the `WARNING: gh wrapper failed auth status` line — leave the SA permissions intact for normal use, but confirm the warning path works by reviewing the entrypoint code path.

- [ ] **Step 7: Open a PR**

```bash
gh pr create --base feat/ws-project-scaffolding --title "Drop GH_TOKEN env var, install /usr/local/bin/gh wrapper" --body "$(cat <<'EOF'
## Summary
- Replaces the GH_TOKEN-to-/etc/environment write with a `/usr/local/bin/gh` wrapper that fetches the PAT from Secret Manager on each invocation.
- Removes the now-redundant GH_TOKEN sed cleanup from the Claude pre-cleanup block.
- Documents the wrapper in README and removes stale GH_TOKEN references.

Fixes the boot-order race where terminals spawned from code-oss never inherited `GH_TOKEN`, and removes the plaintext PAT from `/etc/environment` (mode 0644).

Spec: `docs/superpowers/specs/2026-04-23-drop-gh-token-env-var-design.md`

## Test plan
- [x] `which gh` → `/usr/local/bin/gh`
- [x] `printenv GH_TOKEN` → unset
- [x] `grep ^GH_TOKEN= /etc/environment` → no match
- [x] `gh pr list` succeeds with no `gh auth login` step
- [x] `gh auth status` shows the PAT as active credential
- [x] PAT rotation picked up on next `gh` call with no restart

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

Expected: PR URL printed.
