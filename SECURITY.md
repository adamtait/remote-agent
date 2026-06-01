# Security Policy

## Reporting a vulnerability

Please report security vulnerabilities **privately** — do not open a public
GitHub issue for them.

Use GitHub's [private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)
("Report a vulnerability" under the repository's **Security** tab). We aim to
acknowledge reports within a few business days.

Please include enough detail to reproduce: affected version/commit, the command
or configuration that triggers it, and the impact you observed.

## Design notes relevant to security

`ra` is built around a **zero-secrets-in-config** model:

- Credentials never appear in `config.yaml`. Each secret has a logical name and
  an env-var name in config; the actual value lives only in GCP Secret Manager.
- The workstation's service account is granted `secretmanager.secretAccessor`
  and fetches secret values at boot. Anyone who can read the workstation config
  learns *which* secrets are in use, but not their values.
- Audit secret access via Secret Manager's data-access audit logs.

When reporting, please keep in mind that the threat model assumes the GCP
project and its IAM are themselves trusted and correctly configured.
