#!/usr/bin/env python3
"""Fail if a dev-image workflow can publish a tag it should not.

The publish decision is the only thing standing between a build and a permanent
registry tag. It lives in two places: the reusable `.github/workflows/dev-image.yml`
that every component's `dev-image-<component>.yml` calls, and the self-contained
`dev-image-pi.yml`. It has already leaked
twice: `:dev` was written by main builds (PR #182), and any `workflow_dispatch` -
including one from an unreviewed feature branch - published because the steps only
asked "is this a pull request?".

Those are shell conditions inside a YAML `run:` block, so nothing checks them
until a build runs. This does, on every change to a workflow: it executes the real
step body the way the runner would (expressions substituted, `$GITHUB_OUTPUT`
parsed) across every event/branch combination, and asserts the decision. The
callers carry no decision of their own, so for them it checks the wiring that
would bypass it: what they delegate to, and what they forward.

Run from the repository root: python3 scripts/test-dev-image-workflows.py
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

import yaml

SHA = "0ae751bc1234567890"
WORKFLOWS = Path(".github/workflows")
REUSABLE = "dev-image.yml"
TEST_IMAGE = "dpinsel/ainsel-test"

# event name, GITHUB_REF, the `publish` dispatch input, expected decision
CASES = [
    # The trunk is the integration branch: it owns :dev and the variant floats.
    ("push", "refs/heads/main", "", "true"),
    # A push from a branch the trigger cannot even match: the shell must still
    # refuse to name :dev or the variant floats, whatever it decides to publish.
    ("push", "refs/heads/feat-x", "", "true"),
    ("pull_request", "refs/pull/99/merge", "", "false"),
    # Defensive: a pull_request never carries a trunk ref in practice, but if
    # one ever did, the event gate must still refuse to publish - and with it
    # the variant floats, which additionally require the trunk ref.
    ("pull_request", "refs/heads/main", "", "false"),
    # A dispatch with no answer means "do not publish".
    ("workflow_dispatch", "refs/heads/feat-x", "", "false"),
    ("workflow_dispatch", "refs/heads/feat-x", "false", "false"),
    # An explicit opt-in publishes the base image, and nothing else.
    ("workflow_dispatch", "refs/heads/feat-x", "true", "true"),
    ("workflow_dispatch", "refs/heads/main", "true", "true"),
]


def parse_github_output(text: str) -> dict[str, str]:
    """Read `$GITHUB_OUTPUT`, including the `key<<DELIM` multiline form."""
    out: dict[str, str] = {}
    lines = text.splitlines()
    i = 0
    while i < len(lines):
        line = lines[i]
        match = re.match(r"^([A-Za-z_][A-Za-z0-9_]*)<<(\w+)$", line)
        if match:
            key, delim, buf = match.group(1), match.group(2), []
            i += 1
            while i < len(lines) and lines[i] != delim:
                buf.append(lines[i])
                i += 1
            out[key] = "\n".join(buf)
        elif "=" in line:
            key, value = line.split("=", 1)
            out[key] = value
        i += 1
    return out


def expand(text: str, values: dict[str, str]) -> str:
    """Substitute the `${{ ... }}` expressions this script knows how to evaluate.

    Anything left over is an expression the cases do not cover, so the step's
    behaviour under it would be untested - refuse instead of guessing.
    """
    for expr, value in values.items():
        text = text.replace("${{ " + expr + " }}", value)
    if "${{" in text:
        raise ValueError(f"unevaluated expression in metadata step: {text!r}")
    return text


def pr_number(event: str, ref: str) -> str:
    """What github.event.pull_request.number holds: set on PR events only."""
    if event == "pull_request" and ref.startswith("refs/pull/"):
        return ref.split("/")[2]
    return ""


def run_meta_step(
    step: dict, event: str, ref: str, publish_input: str, inputs: dict[str, str]
) -> dict[str, str]:
    """Execute a `Compute image metadata` body as the runner would."""
    values = {f"inputs.{k}": v for k, v in inputs.items()}
    values["inputs.publish"] = publish_input
    values["github.event.pull_request.number"] = pr_number(event, ref)
    script = expand(step["run"], values)
    step_env = {k: expand(str(v), values) for k, v in (step.get("env") or {}).items()}
    ref_name = ref.removeprefix("refs/heads/").removeprefix("refs/pull/")
    with tempfile.NamedTemporaryFile("w+", suffix=".out") as handle:
        env = dict(
            os.environ,
            **step_env,
            GITHUB_EVENT_NAME=event,
            GITHUB_SHA=SHA,
            GITHUB_REF=ref,
            GITHUB_REF_NAME=ref_name,
            GITHUB_OUTPUT=handle.name,
        )
        subprocess.run(["bash", "-ef", "-c", script], env=env, check=True)
        return parse_github_output(handle.read())


def check_workflow(path: Path, inputs: dict[str, str] | None = None) -> list[str]:
    doc = yaml.safe_load(path.read_text())
    steps = doc["jobs"]["build"]["steps"]
    problems: list[str] = []

    meta = next((s for s in steps if s.get("name") == "Compute image metadata"), None)
    if meta is None:
        return [f"{path.name}: no `Compute image metadata` step to gate on"]
    login = next((s for s in steps if s.get("name") == "Login to Docker Hub"), None)
    if login is None:
        return [f"{path.name}: no `Login to Docker Hub` step?"]
    if login.get("if") != "steps.meta.outputs.publish == 'true'":
        problems.append(f"login is guarded by {login.get('if')!r}, not by the publish output")

    builds = [s for s in steps if "build-push-action" in str(s.get("uses", ""))]
    if not builds:
        return [f"{path.name}: no build-push-action step found"]
    for step in builds:
        with_ = step.get("with") or {}
        push, load = str(with_.get("push")), str(with_.get("load"))
        if "github.event_name" in push + load:
            problems.append(
                f"{step.get('name')}: derives push/load from the event name, "
                "which publishes on any non-PR event including a stray dispatch"
            )
        if "steps.meta.outputs.publish" not in push:
            problems.append(f"{step.get('name')}: push={push!r} ignores the publish decision")

    for event, ref, publish_input, want in CASES:
        got = run_meta_step(meta, event, ref, publish_input, inputs or {})
        if got.get("publish") != want:
            problems.append(
                f"{event} on {ref} (publish input {publish_input or '(unset)'}) "
                f"decided publish={got.get('publish')!r}, want {want!r}"
            )
        # :dev is what the dev cluster runs; only the trunk may name it.
        has_dev = ":dev" in got.get("tags", "")
        if has_dev != (ref == "refs/heads/main"):
            problems.append(f"{event} on {ref}: :dev in tags = {has_dev}")
        if path.name == "dev-image-pi.yml":
            problems += check_pi_variants(got, ref, problems)
            problems += check_pi_base(got, event, ref)
        if path.name == REUSABLE:
            problems += check_reusable_outputs(got, event, ref, inputs or {})
    return problems


def check_reusable_outputs(
    got: dict[str, str], event: str, ref: str, inputs: dict[str, str]
) -> list[str]:
    """The shared workflow names exactly the caller's image, and stamps the build."""
    problems: list[str] = []
    short = SHA[:7]
    want_tags = {f"{TEST_IMAGE}:{short}"}
    if ref == "refs/heads/main":
        want_tags.add(f"{TEST_IMAGE}:dev")
    got_tags = set(got.get("tags", "").splitlines())
    if got_tags != want_tags:
        problems.append(f"{event} on {ref}: tags {sorted(got_tags)}, want {sorted(want_tags)}")

    if inputs.get("stamp_version") == "true":
        if event == "pull_request":
            version = f"pr-{pr_number(event, ref)}-{short}"
        else:
            version = f"{ref.removeprefix('refs/heads/')}-{short}"
        want_args = [f"VERSION={version}", f"VCS_REF={SHA}"]
    else:
        want_args = []
    got_args = got.get("build_args", "").splitlines()
    if got_args != want_args:
        problems.append(
            f"{event} on {ref} (stamp_version={inputs.get('stamp_version')}): "
            f"build_args {got_args}, want {want_args}"
        )
    return problems


def check_reusable_workflow() -> list[str]:
    """Run the shared decision over every case, with and without the version stamp."""
    path = WORKFLOWS / REUSABLE
    if not path.exists():
        return [f"{REUSABLE} is gone - every dev-image caller delegates to it"]
    problems: list[str] = []
    for stamp in ("true", "false"):
        problems += check_workflow(path, {"image_name": TEST_IMAGE, "stamp_version": stamp})

    doc = yaml.safe_load(path.read_text())
    if set(doc[True]) != {"workflow_call"}:
        problems.append(f"triggers {sorted(doc[True])}: must be workflow_call only")
    publish = doc[True]["workflow_call"]["inputs"].get("publish", {})
    if publish.get("default") is not False:
        problems.append(f"`publish` input defaults to {publish.get('default')!r}, want false")
    build = next(s for s in doc["jobs"]["build"]["steps"] if "build-push-action" in str(s.get("uses")))
    with_ = build.get("with") or {}
    if with_.get("tags") != "${{ steps.meta.outputs.tags }}":
        problems.append(f"build tags={with_.get('tags')!r}, not the tags the gate computed")
    if with_.get("build-args") != "${{ steps.meta.outputs.build_args }}":
        problems.append(f"build-args={with_.get('build-args')!r}, not the metadata step's")
    return problems


def check_caller(path: Path) -> list[str]:
    """A component workflow decides nothing itself, so check what would bypass the gate.

    It must delegate to the shared workflow, forward the dispatch `publish` answer
    as-is (never a literal true), keep its own concurrency group, and re-run
    when either its own file or the shared logic changes.
    """
    doc = yaml.safe_load(path.read_text())
    problems: list[str] = []
    jobs = doc.get("jobs") or {}
    job = jobs.get("build") or {}
    if list(jobs) != ["build"] or job.get("uses") != f"./.github/workflows/{REUSABLE}":
        return [f"jobs {list(jobs)} do not delegate to {REUSABLE} as the single `build` job"]
    if job.get("secrets") != "inherit":
        problems.append("does not pass `secrets: inherit` - a publishing run could not log in")

    with_ = job.get("with") or {}
    reusable = yaml.safe_load((WORKFLOWS / REUSABLE).read_text())[True]["workflow_call"]["inputs"]
    unknown = set(with_) - set(reusable)
    if unknown:
        problems.append(f"passes inputs {sorted(unknown)} that {REUSABLE} does not declare")
    if with_.get("publish") != "${{ inputs.publish == true }}":
        problems.append(
            f"publish={with_.get('publish')!r} - forward the dispatch input, "
            "`${{ inputs.publish == true }}`, nothing else"
        )
    image = str(with_.get("image_name", ""))
    if not image.startswith("dpinsel/ainsel-"):
        problems.append(f"image_name={image!r} is outside the dpinsel/ainsel-* namespace")
    dockerfile = str(with_.get("dockerfile", ""))
    if not Path(dockerfile).is_file():
        problems.append(f"dockerfile={dockerfile!r} does not exist")

    on = doc[True]
    dispatch = ((on.get("workflow_dispatch") or {}).get("inputs") or {}).get("publish") or {}
    if dispatch.get("type") != "boolean" or dispatch.get("default") is not False:
        problems.append(f"workflow_dispatch `publish` input is {dispatch!r}, want a boolean defaulting to false")
    group = (doc.get("concurrency") or {}).get("group")
    if group != "${{ github.workflow }}-${{ github.ref }}":
        problems.append(f"concurrency group {group!r} - must be per-workflow, per-ref")
    for event in ("push", "pull_request"):
        paths = (on.get(event) or {}).get("paths") or []
        for own in (f".github/workflows/{path.name}", f".github/workflows/{REUSABLE}"):
            if own not in paths:
                problems.append(f"{event} paths miss {own}")
    return problems


def check_pi_base(got: dict[str, str], event: str, ref: str) -> list[str]:
    """A variant must not FROM a tag that does not exist when it builds.

    buildx resolves FROM from the registry, not the runner's image store, so a
    build-only run has no :<sha> of its own to build on. Getting this wrong fails
    the build (learned from run 35830189618); the alternative - falling back to a
    floating tag - builds something whose base is not the one just compiled, which
    is only acceptable when nothing is being published from it.
    """
    published = got.get("publish") == "true"
    want = SHA[:7] if published else "dev"
    got_base = got.get("base_tag")
    if got_base != want:
        return [
            f"{event} on {ref}: variants build on base_tag={got_base!r}, want {want!r} "
            f"({'this run pushed :<sha>' if published else 'nothing was pushed, so :<sha> does not exist'})"
        ]
    return []


def check_pi_variants(got: dict[str, str], ref: str, seen: list[str]) -> list[str]:
    """The pi variants must never publish a per-build tag, and must build on the
    base produced by this same run."""
    problems: list[str] = []
    variants = got.get("go_tags", "") + "\n" + got.get("maui_tags", "")
    if re.search(r"\d+\.\d+-[0-9a-f]{7}", variants):
        problems.append(
            f"{ref}: variants publish a <float>-<sha> tag ({variants!r}) - "
            "~3 GB of provenance nothing pulls, and 88 GB of the registry"
        )
    published = got.get("publish_variants") == "true"
    floats = {"dpinsel/ainsel-pi-go:1.24", "dpinsel/ainsel-pi-maui:8.0"}
    if published and set(variants.splitlines()) != floats:
        problems.append(f"{ref}: publish_variants=true but tags are {variants!r}")
    if not published and "local-check" not in variants:
        problems.append(f"{ref}: variants are not being published but tags are {variants!r}")
    if published != (ref == "refs/heads/main" and got.get("publish") == "true"):
        problems.append(f"{ref}: publish_variants={got.get('publish_variants')!r}")
    return problems


def check_pi_workflow() -> list[str]:
    path = WORKFLOWS / "dev-image-pi.yml"
    if not path.exists():
        return ["dev-image-pi.yml is gone"]
    doc = yaml.safe_load(path.read_text())
    steps = doc["jobs"]["build"]["steps"]
    problems: list[str] = []
    for name in ("Build and push Go variant", "Build and push .NET MAUI variant"):
        step = next((s for s in steps if s.get("name") == name), None)
        if step is None:
            problems.append(f"{name} step is gone")
            continue
        if step.get("if") is not None:
            problems.append(
                f"{name}: if={step.get('if')!r} - the trunk is what must build and "
                "publish the variants; whether to push is decided by publish_variants"
            )
        args = str((step.get("with") or {}).get("build-args"))
        if "BASE_TAG=${{ steps.meta.outputs.base_tag }}" not in args:
            problems.append(
                f"{name}: build-args={args!r} - the base must come from the same step "
                "that decides whether it was published, not a hardcoded tag"
            )
    return problems


def main() -> int:
    if not WORKFLOWS.is_dir():
        print("Run from the repository root.", file=sys.stderr)
        return 2
    results: list[tuple[str, list[str], str]] = [
        (REUSABLE, check_reusable_workflow(), f"publish gate holds for {len(CASES)} event/branch cases"),
    ]
    for path in sorted(WORKFLOWS.glob("dev-image-*.yml")):
        if path.name == "dev-image-pi.yml":
            problems = check_workflow(path) + check_pi_workflow()
            results.append((path.name, problems, f"publish gate holds for {len(CASES)} event/branch cases"))
        else:
            results.append((path.name, check_caller(path), f"delegates to {REUSABLE}"))
    failed = False
    for name, problems, ok in results:
        if problems:
            failed = True
            print(f"FAIL {name}")
            for problem in problems:
                print(f"      {problem}")
        else:
            print(f"ok   {name}: {ok}")
    if failed:
        print(
            "\nA dev-image workflow can write a permanent registry tag; see "
            "CONTRIBUTING.md for the tag policy.",
            file=sys.stderr,
        )
        return 1
    print("\nAll dev-image workflows gate publishing on an explicit decision.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
