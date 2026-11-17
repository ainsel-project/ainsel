# Registry maintenance

The images this project builds are published to its own container registry
namespace, `ghcr.io/ainsel-project/ainsel-*` (see
[`CONTRIBUTING.md`](../CONTRIBUTING.md#where-images-come-from)). While the old
Docker Hub tags still have users on them, the same builds are mirrored to a
Docker Hub namespace — `dpinsel/ainsel-*` today, chosen by the
`DOCKERHUB_NAMESPACE` repository variable.

This page is the retention policy for the **Docker Hub mirror**: it has no
server-side retention for a personal namespace, so nothing in the pipeline ever
deleted a tag and the registry only ever grew — **456 tags / 125.71 GB across 11
repositories, 91% of the bytes in the three `pi*` images**. Those figures are a
historical measurement of the `dpinsel` account, not a live number: every report
the tooling prints is measured from the namespace you point it at.

GHCR is a separate problem and is not covered by this tooling — see
[After the mirror goes away](#after-the-mirror-goes-away).

Related: [`CONTRIBUTING.md`](../CONTRIBUTING.md) (CI overview, release flow),
[`docs/deployment.md`](deployment.md) (installing the chart),
[`pi/README.md`](../pi/README.md) (pi image variants).

## Tag taxonomy

Every tag in a Docker Hub mirror namespace belongs to exactly one class. If you add a
workflow that writes a new shape, add it here and to the patterns in
`scripts/hub-retention.py` — an unrecognised tag is *never* deleted, but it is
reported as `UNKNOWN`, and the goal is to keep that list empty.

| class | shape | written by | collected? |
|---|---|---|---|
| build | `:<short-sha>` (7 hex) | `dev-image-*.yml` on push to `main` | yes |
| variant build | `<major.minor>-<short-sha>`, e.g. `1.24-0ae751b` | `dev-image-pi.yml` | yes |
| mutable | `:dev` | `dev-image-*.yml`, `main` only | **never** |
| float | `:1.24`, `:8.0` | `dev-image-pi.yml` | **never** |
| cache | `buildcache`, `buildcache-v2` | no workflow — dead since `b66f4adc` (2026-07-20) | yes, see below |
| release | `:X.Y.Z`, `:vX.Y.Z`, `<float>-X.Y.Z` | `release.yml` | only by an explicit purge |
| `:latest` | `:latest` | `release.yml` | **never** |

`:dev` is what a dev install that tracks `:dev` runs; `:latest` and the semver
tags are what users pull. Everything else exists for provenance — the record of
which commit produced which image — and no automated consumer resolves them.

## Policy

Enforced by `scripts/hub-retention.py`, scheduled by
`.github/workflows/maintenance-untag.yml`:

- a build or variant-build tag becomes collectable after **30 days**
- the **newest 20** build tags per repo are always kept, regardless of age, so a
  recent image can always be pulled back without rebuilding
- the **newest 5** variant-build tags per repo are kept, not 20: `pi-maui` is
  ~3 GB per tag and nothing has ever pulled it by that tag, so 20 would pin about
  60 GB of provenance
- every other class is protected. Protected means *not deletable by this tool*,
  including on a purge

The two floors are per class on purpose. Ranking is also per class, so a repo's
protected tags cannot push a build's own tag out of its window.

**Cache tags are judged by evidence, not age.** A `buildcache*` tag is only
worth keeping while something writes it, so the tool reads `.github/workflows/`
for a `cache-to: type=registry:` line and protects the tags only if it finds one.
No workflow configures a registry cache today — every build is cold, and the four
`buildcache` / `buildcache-v2` tags left on the frontend and agent-operator repos
are leftovers of the pre-migration pipeline (~2.4 GB). A checkout whose workflows
cannot be read is treated as live (fail-protective), and `--keep-cache-tags`
forces protection. If caching is ever added back, the tags protect themselves
again with no change here.

At the time of writing, the default policy collects **223 of 456 tags, 86.38 GB**
(logical, an upper bound — layers shared with kept tags are not actually freed
until the registry garbage-collects them).

## Running it

From the repository root. Listing is anonymous (the repos are public), so a
report needs no credentials:

```bash
python3 scripts/hub-retention.py                       # whole namespace, dry run
python3 scripts/hub-retention.py --repo ainsel-pi-maui # one repo
python3 scripts/hub-retention.py --namespace my-org    # a different mirror account
python3 scripts/hub-retention.py --keep-cache-tags     # protect buildcache* anyway
python3 scripts/hub-retention.py --json | jq 'to_entries[].value | map(select(.disposition == "delete")) | length'
```

The account is a parameter, not a constant: `--namespace` (or
`DOCKERHUB_NAMESPACE`) picks it, defaulting to the historical `dpinsel` mirror.
Point it at your own account and the same policy and the same guards apply there.

Deletion is opt-in and needs a token that can delete images:

```bash
export DOCKERHUB_NAMESPACE=dpinsel    # the account to clean
export DOCKERHUB_USERNAME=...         # the account the token belongs to
export DOCKERHUB_TOKEN=...            # personal access token, delete:delete-image
python3 scripts/hub-retention.py --apply
```

`--apply` refuses to run on a policy that looks like a fat-fingered flag
(`--max-age-days 0`, `--keep-newest 0`, a malformed repo name). A dry run accepts
anything, so you can explore what a threshold would collect before authenticating.

Run the tests before changing any of the patterns:

```bash
python3 -m unittest scripts.test_hub_retention -v
```

## In CI

`.github/workflows/maintenance-untag.yml`

| trigger | what happens |
|---|---|
| `schedule` (Mon 05:17 UTC) | unit tests, then a **report-only** pass; the tag list lands in the run summary and a warning is raised if anything is collectable |
| `workflow_dispatch` | same, plus every threshold as an input; deletes only if `apply` is checked |
| `pull_request` touching the script or policy docs | unit tests only; the registry is untouched |

The cron never deletes. That is deliberate: an unattended job must not be able to
take an image out from under a running deployment, and a deletion should always
have a human who looked at a list first.

## First release after the namespace switch

**Two of these gates come before the merge, not after it.** Every `dev-image-*` run and
every release stops writing `dpinsel/ainsel-*` the moment this lands, because a mirror
push happens only while `vars.DOCKERHUB_NAMESPACE` is set — so either set that variable
first (the mirror never gaps) or make sure users are told the mirror stops, which
[`docs/upgrade-guide.md`](upgrade-guide.md) does. And no chart release may ship with the
default `global.imageRegistry` until the ten packages below exist and are publicly
pullable: that default is exactly what makes a fresh install pull from GHCR.

GHCR packages start **private**, and a private package is pullable by CI (which
authenticates) and by nobody else — so a green release run does not prove users can
pull. After the first release published to `ghcr.io/ainsel-project`:

1. Set the org/package default to public, or flip each package individually under
   **Packages → `<name>` → Package settings → Danger Zone → Change visibility**.
   That is the chart plus the ten image packages:
   `ainsel-hub-frontend`, `ainsel-hub-backend`, `ainsel-k8s-ai-agent-operator`,
   `ainsel-k8s-event-source-gateway-operator`, `ainsel-mcp`, `ainsel-chat-mcp`,
   `ainsel-webhook-receiver`, `ainsel-pi`, `ainsel-pi-go`, `ainsel-pi-maui`.
2. Re-run the release's `Verify published artifacts` job, or check anonymously:
   `docker manifest inspect ghcr.io/ainsel-project/ainsel-hub-backend:<version>`
   from a machine with no registry login. Authenticated success proves nothing.
3. Set `vars.DOCKERHUB_NAMESPACE` to the mirror account (e.g. `dpinsel`) for as
   long as the mirror is wanted. Unset means no mirror is pushed at all — which is
   the correct state for a fork with no Docker Hub credentials.

## After the mirror goes away

GHCR has an org-level cleanup policy for **untagged** manifests. It does nothing for
the tags this project writes: `:<short-sha>` build tags and the pi
`<float>-<sha>` variant tags are *tagged*, and the variants cost ~3 GB each.

So when the Docker Hub mirror is dropped, retention does not move with it by
itself — `scripts/hub-retention.py` speaks the Docker Hub API and would be cleaning
a namespace that no longer receives anything. Porting it to the GHCR packages API is
tracked separately; do not treat the mirror's sunset as the end of the policy.

## Purging pre-pipeline release tags

The `release-please` pipeline has produced exactly one run, and it failed before
publishing anything, so there is **no release on GitHub** (`git tag -l` is empty)
while Docker Hub carries `0.1.0` on eight repos plus `0.2.0`, `0.2.1`, `0.3.0` on
the frontend. Those tags came from the Forgejo-era pipeline: they are two months
stale, mutually inconsistent per component, and `:latest` on several repos points
at them.

They are also what `chart/values.yaml` pins, which is why the purge is a separate,
deliberate step rather than part of the weekly pass:

```bash
python3 scripts/hub-retention.py --include-release-tags --yes \
  --latest-release <version of a published release> --apply
```

Three guards have to pass before a single tag is deleted:

1. `--include-release-tags` also requires `--yes`.
2. `--latest-release` is required and must be a version — the tool will not
   "purge" release tags when it cannot prove a newer release exists.
3. A tag is only collected if the supplied release is strictly newer, so the
   current release always survives its own purge.

Do not run it until a real release is published *and* `chart/values.yaml` no
longer pins the old tags (see "Known rough edge: chart image tags" in
[`CONTRIBUTING.md`](../CONTRIBUTING.md)).

## Rotation notes

`secrets.DOCKERHUB_TOKEN` is used by the build workflows to log in and push.
Deleting a tag through the Hub API is a distinct scope (`delete:delete-image` on a
granular personal access token), so the existing token may 403 on a first apply.
Create the token at <https://hub.docker.com/settings/security>, and keep it read
plus write plus delete.

`verify_credentials()` calls `GET /v2/user/` before touching anything, so a wrong
or under-scoped token fails with a message instead of half-applying a policy.

## Pull rate limits

This section is about the **Docker Hub mirror**. It stays relevant while
`vars.DOCKERHUB_NAMESPACE` is set and while users still pull `dpinsel/ainsel-*`;
the chart's default registry is GHCR, and the two registries do not share a budget.

Docker Hub's [usage and limits](https://docs.docker.com/docker-hub/download-rate-limit/)
table gives a Personal account **200 pulls per six hours when authenticated** and
**100 per six hours per IPv4 address (or IPv6 /64) when not**, "subject to fair
use".

An earlier draft of this page estimated that a dev deployment polling the `:dev`
digests alone spent "about 96 of the 100" anonymous budget every six hours. **That estimate is
wrong, and wrong in the useful direction** - measured against the live registry on
2026-09-23 from a single egress IP:

| what was sent | requests | `ratelimit-remaining` |
| --- | --- | --- |
| `HEAD .../manifests/dev` — exactly what the digest check does | 12 | `100` → `100`, unmoved |
| `GET` a config blob + a layer blob | 2 | `100` → `99` |
| `GET` manifest + one layer, three different repos | 6 | `99` → `97` |

A `HEAD` manifest probe is **not billed at all**, and a billed pull costs roughly
one unit per image rather than one per request: an image with 13 layers did not
turn into 13 units. The `docker-ratelimit-source: <ip>` header confirms attribution
is per address while anonymous.

That leaves the budget real headroom, so **the cluster deliberately keeps pulling
anonymously**: no `imagePullSecrets`, no credential to rotate, nothing in the chart
to keep honest.

Revisit it if either of these shows up, since they are the only ways this becomes a
problem:

- `kubectl describe pod` events in your install's namespace show `429` /
  `toomanyrequests` - the shared-address case, where several agents' pods pull
  fresh layers at once through one node's egress IP
- a deploy pipeline that pins `:dev` digests reports failed digest lookups. Such
  lookups usually fail open, so a throttled registry silently stops pinning new
  digests instead of breaking loudly

The fix in both cases has the same shape: a `kubernetes.io/dockerconfigjson`
secret in the namespace, referenced by `imagePullSecrets`, which the chart already
accepts per component (`chart/values.yaml`, default `[]`). Authenticated pulls
bill the account's 200 rather than the address's 100. Do it on that evidence, not
on arithmetic like the estimate above.

**GHCR has the same shape of limit and no measurement of ours.** GitHub documents
200 requests per hour per IP address for anonymous pulls and 5000 per hour for an
authenticated account, against
[its own registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry).
That is closer to the ceiling a digest-polling dev cluster lives under than Docker
Hub's anonymous 100-per-six-hours, so the same evidence applies: if `:dev` digest
lookups start failing on a GHCR install, add a pull secret rather than guessing at
arithmetic. The chart accepts one per component; nothing about the registry switch
changed how you configure it.
