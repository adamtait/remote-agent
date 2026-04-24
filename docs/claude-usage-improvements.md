# Claude Usage Improvement Plan

Generated from conversation history analysis (2026-04-24).

## Summary

Usage is structured and technically sophisticated. Biggest drag: ~20% of history entries are
session setup overhead (`/reload-plugins` ×17, `/superpowers:using-superpowers` ×13).
Second issue: brainstorms overshoot scope, generating options that get immediately discarded.
No `CLAUDE.md` exists in the workspace, so project context is re-established from scratch
every session.

---

## Findings

| Area | Issue | Example | Fix |
|------|-------|---------|-----|
| Session overhead | `/reload-plugins` run 17× — 11% of all entries | Appears before nearly every new feature session | Add SessionStart hook to auto-reload, or diagnose why plugins drop state |
| Orientation ritual | `/superpowers:using-superpowers` run 13× | Runs before every new worktree and session | Create `CLAUDE.md` encoding project context so Claude arrives oriented |
| Plan scope overshoot | Brainstorms generate 2–3 options; half immediately discarded | "drop plan 2. only complete plan 1" | Constrain brainstorms upfront: state preferred approach, ask for validation not alternatives |
| Repeated debugging | Same `ws create` scaffolding issue debugged across multiple sessions | "manual testing reveals ws create STILL asks..." | After a fix, include explicit end-to-end verify step: `run ws setup && ws create from a clean temp dir` |
| Log pasting friction | Same log pasted twice into duplicate systematic-debugging entries | `[Pasted text #1 +11 lines]` in two near-identical calls | When restarting a debug session, add what changed: "previous session tried X, still failing, new log:" |
| No CLAUDE.md | Zero project context persisted in repo | Every session requires orientation ritual | Run `/init`, then extend with `ws`-specific invariants |
| Unused skills | `finishing-a-development-branch` and `verification-before-completion` installed but never invoked | PR workflow spans 3–5 separate messages per branch | Use these skills — no setup required |

---

## Top 3 Immediate Changes

### 1. Create `CLAUDE.md`

Eliminates the `/superpowers:using-superpowers` ritual entirely. Should include:
- What `ws` does and its CLI structure
- Sole-user / no-migration invariant
- Config precedence: CLI args > `.ws/` > `~/.config/ws/`
- Standard worktree-based branch workflow

Run `/init` to generate a base, then append project-specific invariants.

### 2. Fix or automate `/reload-plugins`

17 invocations is abnormal. Either diagnose why plugins drop between sessions, or add to
session start hook so it fires automatically:

```json
"hooks": {
  "SessionStart": [{ "type": "command", "command": "claude plugin reload" }]
}
```

### 3. Constrain brainstorms to avoid plan waste

Current pattern generates options you don't want:

**Before:**
```
/superpowers:brainstorming [open-ended problem]
→ 3 plans → "drop plan 2"
```

**After:**
```
/superpowers:brainstorming [problem]. I'm leaning toward [approach]. Validate it, identify
the top 2 risks, and propose implementation steps. Skip alternatives unless mine is
fundamentally broken.
```

---

## Claude Code Extensions

### 1. `CLAUDE.md` — immediate, highest ROI

A ~20-line file covering project purpose, CLI structure, the no-migration rule, config
layering, and the standard branch→PR→delete workflow eliminates ~10% of history entries.

### 2. `finishing-a-development-branch` skill — already installed, unused

PR workflow currently spans 3–5 separate messages per branch (commit, push, open PR, merge,
delete). The superpowers skill consolidates this. No setup required — start using it.

### 3. GitHub MCP server

Heavy `gh` usage currently requires Bash permission prompts per command. The GitHub MCP
server gives Claude native PR/issue/branch access:

```bash
claude mcp add github -- docker run -i --rm \
  -e GITHUB_PERSONAL_ACCESS_TOKEN \
  ghcr.io/github/github-mcp-server
```

### 4. Auto-verify hook (post-implementation)

Repeated "still broken after fix" pattern suggests fixes complete without end-to-end smoke
testing. A `PostToolUse` hook on `Edit`/`Write` for Go files catches compile errors immediately:

```json
"PostToolUse": [{
  "matcher": "Edit|Write",
  "command": "cd /home/user/workspace && make build 2>&1 | tail -5"
}]
```

---

## Long-term Habit

Before opening any new debug session for a recurring issue, write one sentence about what
the previous attempt tried and why it failed. The duplicate `systematic-debugging` entries
(same log pasted twice) suggest time is lost re-running identical approaches instead of
arriving with new hypotheses.
