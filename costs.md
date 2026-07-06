# Cloud Workstation Cost Estimate

> **Source of figures:** Actual billing records were inaccessible in this environment
> (authenticated only as the `ra-workstation-sa` service account, Cloud Billing API
> disabled on the project, no BigQuery billing export). The numbers below are computed
> from GCP's current published rates applied to the exact resource specs `ra` provisions
> (read from `~/.config/ra/config.yaml` and the code). Rates as of July 2026.

## Configured workstation specs

- **Machine:** `e2-standard-2` (2 vCPU / 8 GB) in `us-central1`
- **Disk:** 50 GB `pd-balanced` (persistent home disk)
- **Auto-sleep:** 3 h idle · **Auto-shutdown:** 10 h max continuous run

## Cost while a workstation is *running*

Billed per-minute only while running; all three components stop when it auto-sleeps.

| Component | Rate | Per running hour |
|---|---|---|
| Workstations management fee | $0.05 / vCPU-hr × 2 vCPU | $0.100 |
| Compute Engine VM (e2-standard-2) | ~$0.067 / hr | $0.067 |
| **Total** | | **~$0.167 / running hour** |

**Average for a typical work day** (~8 running hours — active coding plus short idle
tails before the 3 h auto-sleep kicks in):

| Scenario | Running hrs | Compute + mgmt cost |
|---|---|---|
| Light day | 4 h | ~$0.67 |
| **Typical day** | **8 h** | **~$1.34** |
| Heavy (hits 10 h shutdown cap) | 10 h | ~$1.67 |

A **typical work day ≈ $1.30–1.35** in running charges, plus the base below.

## Base cost — standing resources, *no running workstations*

These bill continuously whether or not any workstation is awake.

| Resource | Size | Monthly | Per day |
|---|---|---|---|
| `pd-balanced` home disk | 50 GB × $0.10/GiB-mo | $5.00 | $0.164 |
| Artifact Registry (Docker image) | ~3 GB × $0.10/GB-mo | ~$0.25 | ~$0.008 |
| Secret Manager | ~5–8 versions (partly free-tier) | $0–0.36 | ~$0.01 |
| GCS claude-sync bucket | <1 GB standard | ~$0.05 | ~$0.002 |
| **Workstations cluster control plane** | **$0.20/cluster-hr × 24 h** | **$144.00 / cluster** | **$4.80 / cluster** |
| **Total base (1 cluster)** | | **~$149/mo** | **~$5.0 / day** |

**The cluster control plane fee dominates everything else.** It is a fixed $0.20 per
cluster per hour, billed 24/7 regardless of whether any workstation is running — auto-sleep
and auto-shutdown do nothing for it. At one cluster that's **~$144/month ($4.80/day)**;
observed June billing averaged **~$9/day**, consistent with **two clusters** in us-central1
(2 × $4.80 = $9.60/day). The only way to stop this charge is to delete the cluster itself.
By comparison the 50 GB disk (~$0.16/day) and everything else are rounding error, and the
VM/management fees are $0 while asleep.

## Bottom line

- **Base (idle) cost:** **~$5/day per cluster** (~$149/month) — almost entirely the
  $4.80/day control plane fee; the 50 GB disk and other storage add only ~$0.19/day.
- **Typical work day (8 running hrs, 1 cluster):** ~$1.34 running + ~$5.0 base ≈ **~$6.3/day**.
- **Rough monthly** (~22 working days, 1 cluster): ~$29 running + ~$149 base ≈ **~$178/month**.
- **The single biggest lever is cluster count.** Each extra 24/7 cluster adds ~$144/month
  for zero usage. Consolidate to one shared cluster (a cluster holds many configs and
  workstations), and delete the cluster entirely during long idle stretches (`ra` recreates
  it in a few minutes).
- **Use `ra teardown` to stop the fee.** `ra teardown` deletes ra-provisioned resources
  (workstations → config → **cluster** → service account/IAM → secrets → Artifact
  Registry repo) in one command; removing the cluster is what actually ends the
  control-plane charge. Run `ra teardown --dry-run` first to preview, then `ra teardown`.
  Because the cluster (and SA/repo/secrets) are shared across projects, teardown deletes
  them only when the project you run it from is the **last config on the cluster**; pass
  `--delete-shared` to force removal while other projects still use it. GCS buckets
  (synced data) are preserved unless you pass `--delete-buckets`.

## Caveats

- The control plane fee is per **cluster**, not per workstation or per config — many
  workstations under one cluster share the single $0.20/hr charge.
- E2 machines don't get sustained-use discounts — list price applies.
- Bumping `machine_type` to `e2-standard-8` for heavy builds roughly quadruples running
  cost (~$0.67/running hr → ~$5.4 for an 8 h day).
- For *actual* spend: enable the Cloud Billing API and set up a BigQuery billing export
  (or `gcloud billing`), or grant an account with `billing.viewer`.

## Sources

- [Cloud Workstations pricing](https://cloud.google.com/workstations/pricing)
- [Compute Engine VM pricing](https://cloud.google.com/compute/all-pricing)
- [e2-standard-2 rate](https://www.economize.cloud/resources/gcp/pricing/compute-engine/e2-standard-2/)
- [Persistent Disk pricing](https://cloud.google.com/compute/disks-image-pricing)
- [Artifact Registry pricing](https://cloud.google.com/artifact-registry/pricing)
