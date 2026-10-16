# Skill: Docker Hub Tag Retention

**When to use:** when asked to clean up, audit, or explain the images in
`dpinsel/ainsel-*` — or before changing anything about which tags get written.

The policy, the numbers behind it, and the history live in
[`docs/maintenance.md`](../../../docs/maintenance.md). This file is the
executable version: what to run, in what order, and what you may and may not
do without a human.

---

## The one rule

**Deleting a tag is a human decision.** Run reports, propose deletions, show the
list. Do not run `--apply` on your own initiative, and do not run it at all for
release tags without the guardrails below. The scheduled workflow exists and
deliberately never deletes — that is the intended posture, not an oversight.

---

## 1. Report first (no credentials needed)

The repos are public, so a report is anonymous:

```bash
python3 scripts/hub-retention.py                       # whole namespace, dry run
python3 scripts/hub-retention.py --repo ainsel-pi-maui # one repo
python3 scripts/hub-retention.py --keep-cache-tags     # protect buildcache* anyway
python3 scripts/hub-retention.py --json | jq 'to_entries[].value | map(select(.disposition == "delete")) | length'
```

Read the report before quoting a number: it separates *logical* size (an upper
bound — shared layers are not freed until the registry garbage-collects) from
tag counts, and it lists any tag it does not recognise as `UNKNOWN`.

`UNKNOWN` is not noise. An unrecognised tag is never deleted, so a new tag
shape from a new workflow silently escapes the policy. If you added or changed a
workflow that pushes images, add the shape to the taxonomy in
`docs/maintenance.md` **and** to the patterns in `scripts/hub-retention.py`.

## 2. Tests before touching a pattern

```bash
python3 -m unittest scripts.test_hub_retention -v
```

Required before changing `--max-age-days` defaults, the tag patterns, or the
release-purge logic. The suite is the contract for "which tags are collectable".

## 3. Know what is protected

| Class | Shape | Collectable? |
|---|---|---|
| build | `:<short-sha>` | yes, after 30 days, keeping the newest 20 per repo |
| variant build | `<major.minor>-<short-sha>` | yes, same rule |
| mutable | `:dev` | **never** — what the dev namespace runs |
| float | `:1.24`, `:8.0` | **never** |
| release | `:X.Y.Z`, `:vX.Y.Z`, `<float>-X.Y.Z` | only by an explicit purge |
| `:latest` | `:latest` | **never** |
| cache | `buildcache*` | yes (dead tags), unless `--keep-cache-tags` |

## 4. Deletion — only when a human has looked at the list

```bash
export DOCKERHUB_USERNAME=<user>
export DOCKERHUB_TOKEN=<pat>          # needs the delete:delete-image scope
python3 scripts/hub-retention.py --apply
```

- `--apply` refuses a policy that looks like a fat-fingered flag
  (`--max-age-days 0`, `--keep-newest 0`, a malformed repo name). A dry run
  accepts anything, which is why you explore there first.
- The script calls `GET /v2/user/` before deleting anything, so an
  under-scoped token fails with a message instead of half-applying. A 403 on a
  first apply usually means the token lacks `delete:delete-image` — the build
  token is not enough.

## 5. Release-tag purge — a separate, deliberate act

Old release tags may predate the release pipeline and be what
`chart/values.yaml` pins. The purge is guarded three ways and must not be run
until a real release exists *and* the chart no longer pins the old tags:

```bash
python3 scripts/hub-retention.py --include-release-tags --yes \
  --latest-release <published version> --apply
```

`--include-release-tags` requires `--yes`; `--latest-release` is required and
must be a version; a tag is only collected if that release is strictly newer.
Check the pin first:

```bash
grep -n "tag:" chart/values.yaml
git tag -l          # what actually exists as a release
```

## 6. Pull rate limits — evidence, not arithmetic

Docker Hub gives a Personal account 200 authenticated pulls / 6 h and ~100 per
IP anonymous. `HEAD` manifest probes are not billed, so the pipeline's digest
checks cost far less than they look. Act only on one of these:

- `kubectl describe pod` on the dev namespace shows `429` / `toomanyrequests`
- a deploy workflow logs `::warning::digest lookup failed` (it fails open)

The fix is a `kubernetes.io/dockerconfigjson` secret referenced by
`imagePullSecrets` (the chart accepts it per component), which bills the
account's 200 instead of the address's 100.

---

## Common pitfalls

| Pitfall | Why it misleads | Fix |
|---|---|---|
| Reporting logical bytes as freed space | Shared layers stay until the registry GCs | Quote the report's own caveat |
| `--apply` with the build token | 403 half-way through | Granular PAT with `delete:delete-image`; the script verifies credentials first |
| New tag shape added by a workflow | Escapes the policy as `UNKNOWN` forever | Taxonomy + patterns in the same PR |
| Purging release tags that `values.yaml` pins | The cluster can no longer pull what it is configured to run | `grep tag: chart/values.yaml` and `git tag -l` first |
| Deleting `:dev` "to force a fresh image" | It is protected, and the dev namespace depends on it | Re-run the `dev-image-*` workflow instead |
| Treating the weekly cron as the cleanup | It is report-only by design | A human applies after reading the report |
| Adding a deletion to a code PR | Mixing an irreversible action with a code change | Report in the PR, delete separately, by a human |
