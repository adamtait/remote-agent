# Auth and Security

## Rotating secrets

All rotations happen via Secret Manager — no need to re-run `ra create`.

For plugins with command-generated credentials, re-run the plugin install command (same source you installed from) to capture and upload a new secret version:

```bash
ra plugin install https://github.com/adamtait/ra-plugins codex --force
```

```bash
echo -n 'newvalue' | gcloud secrets versions add <secret-name> \
    --data-file=- --project=$PROJECT_ID
```

Rotation behavior depends on how each secret is consumed:
- **`github_pat`** is fetched per-git-operation via a credential helper; rotation is picked up on the next push/pull/clone, no restart.
- **API key plugins** (e.g. claude apikey mode) typically use a helper that re-reads the secret periodically; rotation is transparent within minutes.
- **Other secrets** are fetched at boot and exported to `/etc/environment` + `/run/ra/env`. Rotation requires `ra stop` + `ra create` (or `gcloud workstations start`) to take effect.

## How auth works at runtime

At every workstation boot:

1. The base image's startup machinery runs `/etc/workstation-startup.d/200_remote-agent-setup.sh`.
2. That script iterates `RA_SECRETS` (passed via container env from `ra create`), fetches each from Secret Manager, and writes them to `/etc/environment` + `/run/ra/env`. Plugin scripts in `/etc/workstation-startup.d/` (numbered ≥ 250) run after 200_; they fetch their own helpers and gate on `RA_PLUGIN_<NAME>_ENABLED`. The github plugin, for example, installs a per-git-op credential helper that fetches the PAT fresh from Secret Manager.
3. Other `/etc/workstation-startup.d/` scripts (numbered ≥ 250, contributed by plugins) run independently. Each plugin script sources `/run/ra/env` and gates on `RA_PLUGIN_<NAME>_ENABLED`.
4. The script writes `/run/ra/ready`, unblocking login shells via `/etc/profile.d/00-ra-wait.sh`.

## Security notes

The workstation config carries zero secret values — only secret names and non-sensitive metadata. Anyone with `workstations.configs.get` can read the config and learn *which* secrets are in use, but cannot read the secret values themselves; that requires `secretmanager.secretAccessor`, granted only to the workstation's service account.

To audit credential access, look at Secret Manager's data access audit logs — every fetch (boot-time, per-operation, periodic) appears there.
