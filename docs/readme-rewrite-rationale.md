# README rewrite — change rationale

This note records why the top-level `README.md` was rewritten, so reviewers can
evaluate the decisions rather than diff blindly. It captures the state as of the
rewrite; it is not maintained as living documentation.

## Goal

A developer landing on the repo cold should understand what `ra` does, why it's
worth using, and how to start — within ~30 seconds of scanning. The rewrite is
modeled on the techniques used by [`bat`](https://github.com/sharkdp/bat) and
[`ripgrep`](https://github.com/BurntSushi/ripgrep): lead with the
differentiator, show real output before explaining it, group install by
platform, and present costs/comparisons as scannable tables.

## Key changes and why

- **Benefit-led tagline.** Replaced "simple CLI tool … rock-solid" with "Run
  your CLI coding agents on a throwaway Google Cloud Workstation, not your
  laptop." The project's core thesis is blast-radius isolation, so it leads.
- **Hero example in the first screenful.** The old README showed no output until
  the command lists. The new hero is `ra create`'s *actual* success block
  (verified against `cmd/create.go`); the 11 progress steps are elided with a
  comment rather than fabricated line-by-line.
- **"Design" prose → "Why ra?" list.** Five specific, contrasted advantages,
  each tied to a real mechanism (Secret Manager, service account, plugins, VPC)
  instead of adjectives.
- **Every command and flag verified against source.** Confirmed
  `ra validate --show` (not `--print`), `ra plugin install … --user/--local`
  (not `--project`), and that `ra create` takes only `--auto-sleep` /
  `--auto-shutdown`. Nothing invented; nothing real cut.
- **Honest install section.** From-source is the only supported path today, so
  it's the primary block; missing prebuilt-binary / Homebrew / Windows paths are
  marked with a `<!-- TODO -->` rather than inventing a formula.
- **Real cost table.** Replaced vague "Cost notes" bullets with a table drawn
  from the intro post's billing appendix, foregrounding the cluster
  control-plane fee as the biggest, least obvious lever.
- **~40% shorter (232 → ~138 lines).** Folded the extension-points table,
  runtime-auth walkthrough, plugin reference, and rotation docs into a tight
  "How it fits together" section with links out.

## Post-review adjustments

Applied after the PR #153 code review:

- Restored `ra logs` and `ra delete` to the lifecycle usage block — `delete` is
  distinct from `stop` and had no replacement.
- De-linked the "intro post / cost appendix" reference (it pointed at the repo
  root); left an inline `TODO` to link the real URL once published.
- Recovered the two most useful bits from the removed sections in one bullet
  each: secret rotation semantics, and the `ra plugin et connect` transport.

## Known gaps / follow-ups

- The "intro post" / cost-appendix link points at the repo root as a
  placeholder — swap it for the real blog-post URL when published.
- Prebuilt binaries / Homebrew tap / Windows (WSL) install instructions are
  flagged as TODO in the README and not yet written.
