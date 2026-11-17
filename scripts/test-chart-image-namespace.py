#!/usr/bin/env python3
"""Every project image follows `global.imageRegistry`; upstream images never do.

The chart used to hardcode one maintainer's Docker Hub namespace into eight
image values, so a fork or an air-gapped cluster had to override all eight (and
could not override the two the console reads at runtime without editing
templates). `global.imageRegistry` is now the single prefix, composed by the
`ainsel.image` helper.

This renders the chart and asserts the composition, because the failure mode of
getting it wrong is silent: a component that keeps the old namespace renders
valid YAML, deploys, and pulls an image from a registry the project does not
own - or fails to pull at all once that namespace is gone.

  * Every project image renders under the registry, including the two that are
    only ever handed to another process as an env var (`CONNECTOR_IMAGE_WEBHOOK`
    and `CHAT_MCP_IMAGE`) and therefore compose no pod spec in a template.
  * `postgres` is an upstream image and stays unprefixed under any registry.
  * A repository that already names a registry host is used verbatim, so a
    per-component mirror override is not prefixed a second time.
  * `global.imageRegistry: ""` renders repositories as-is (plain Docker Hub).
  * With default values every project image is tagged with the chart's
    appVersion - the tag the release pipeline publishes - and an explicit tag
    still wins. A fixed tag in values.yaml shipped charts that pulled images
    nobody had published.

Run from the repository root: python3 scripts/test-chart-image-namespace.py
"""

from __future__ import annotations

import os
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
CHART = ROOT / "chart"
HELM = os.environ.get("HELM", "helm")

# mcp is disabled by default; enable it so its image renders too.
BASE_SETS = ["mcp.enabled=true"]

#: Every image this project publishes, as the bare `image.repository` default.
PROJECT_IMAGES = [
    "ainsel-k8s-ai-agent-operator",
    "ainsel-k8s-event-source-gateway-operator",
    "ainsel-hub-backend",
    "ainsel-hub-frontend",
    "ainsel-mcp",
    "ainsel-chat-mcp",
    "ainsel-webhook-receiver",
    "ainsel-pi",
]

#: Rendered `image:` / `value:` strings, which is where a container reference
#: can hide: a template field, or the env var a hub/operator reads to compose a
#: pod spec of its own.
REF_LINE = re.compile(r"""^\s*(?:image|value):\s+"([^"]*)"$""", re.MULTILINE)


def render(value_sets: list[str], values_file: Path | None = None) -> str:
    cmd = [HELM, "template", "ainsel", str(CHART)]
    if values_file is not None:
        cmd += ["-f", str(values_file)]
    for value_set in value_sets:
        cmd += ["--set", value_set]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        sys.exit(f"helm template failed for {value_sets}:\n{result.stderr}")
    return result.stdout


def image_refs(manifests: str) -> list[str]:
    """Every rendered string that carries a container reference."""
    refs: list[str] = []
    for line in REF_LINE.finditer(manifests):
        ref = line.group(1)
        if any(name in ref for name in PROJECT_IMAGES) or ref.startswith("postgres"):
            refs.append(ref)
    return refs


def check(condition: bool, problem: str, problems: list[str]) -> None:
    if not condition:
        problems.append(problem)


def check_prefixed(manifests: str, registry: str, problems: list[str]) -> None:
    """Each project image appears, and only under `registry`."""
    refs = image_refs(manifests)
    for name in PROJECT_IMAGES:
        matching = [r for r in refs if re.search(rf"(^|/){name}[:@]", r)]
        check(bool(matching), f"{name} is not rendered at all", problems)
        for ref in matching:
            if registry:
                check(
                    ref.startswith(f"{registry}/{name}:"),
                    f"{ref!r} is not under {registry!r}",
                    problems,
                )
            else:
                check(
                    ref.startswith(f"{name}:"),
                    f"{ref!r} should be rendered as-is with an empty registry",
                    problems,
                )
    for ref in refs:
        if ref.startswith("postgres"):
            check("/" not in ref.split(":")[0], f"{ref!r}: postgres must stay unprefixed", problems)


def chart_app_version() -> str:
    for line in (CHART / "Chart.yaml").read_text().splitlines():
        if line.startswith("appVersion:"):
            return line.split(":", 1)[1].strip().strip('"')
    sys.exit("chart/Chart.yaml has no appVersion")


def check_tags(manifests: str, tag: str, problems: list[str]) -> None:
    """Each project image reference carries exactly `tag`."""
    for ref in image_refs(manifests):
        if ref.startswith("postgres"):
            continue
        name_and_tag = ref.split("@", 1)[0].rsplit("/", 1)[-1]
        check(
            name_and_tag.endswith(f":{tag}"),
            f"{ref!r} is not tagged {tag!r}",
            problems,
        )


def main() -> int:
    if not CHART.is_dir():
        print("Run from the repository root.", file=sys.stderr)
        return 2

    problems: list[str] = []
    default_registry = "ghcr.io/ainsel-project"

    # The shipped default is the project-owned namespace, not a person's account.
    defaults = render(BASE_SETS)
    check_prefixed(defaults, default_registry, problems)

    # The example overlay composes the same way, so copying it does not
    # reintroduce a hardcoded owner.
    check_prefixed(
        render(BASE_SETS, CHART / "values-example.yaml"),
        default_registry,
        problems,
    )

    # One value retargets everything, including the env-var references the
    # hub and the agent operator pass into pods they compose themselves.
    mirror = "registry.example.com:5000/ainsel-mirror"
    check_prefixed(render(BASE_SETS + [f"global.imageRegistry={mirror}"]), mirror, problems)

    # Empty registry means "the repository is the whole reference" - the plain
    # Docker Hub layout, which is what a mirror of the old tags needs.
    check_prefixed(render(BASE_SETS + ["global.imageRegistry="]), "", problems)

    # A repository that already names a registry host wins over the prefix, and
    # is not prefixed a second time.
    escaped = render(
        BASE_SETS + ["hub.image.repository=mirror.internal/team/ainsel-hub-backend"]
    )
    hub_refs = [r for r in image_refs(escaped) if "ainsel-hub-backend" in r]
    check(
        hub_refs and all(r.startswith("mirror.internal/team/ainsel-hub-backend:") for r in hub_refs),
        f"full-registry repository was rewritten: {sorted(set(hub_refs))}",
        problems,
    )
    # The other components still follow the global prefix.
    for name in PROJECT_IMAGES:
        if name == "ainsel-hub-backend":
            continue
        for ref in image_refs(escaped):
            if re.search(rf"(^|/){name}[:@]", ref):
                check(
                    ref.startswith(f"{default_registry}/{name}:"),
                    f"{ref!r} lost the registry prefix",
                    problems,
                )

    # Default tags follow the release: the chart's appVersion is the one tag the
    # release pipeline is guaranteed to have published for every component.
    check_tags(defaults, chart_app_version(), problems)

    # A pinned tag still wins, for the component it is set on only.
    pinned = render(BASE_SETS + ["hub.image.tag=9.9.9-pinned"])
    hub_refs = [r for r in image_refs(pinned) if "ainsel-hub-backend" in r]
    check(
        hub_refs and all(r.endswith(":9.9.9-pinned") for r in hub_refs),
        f"pinned hub tag not rendered: {sorted(set(hub_refs))}",
        problems,
    )
    check_tags(
        "\n".join(l for l in pinned.splitlines() if "ainsel-hub-backend" not in l),
        chart_app_version(),
        problems,
    )

    if problems:
        print("FAIL image namespace rendering")
        for problem in problems:
            print(f"      {problem}")
        return 1
    print("ok: every project image follows global.imageRegistry and the chart's appVersion; postgres never does")
    return 0


if __name__ == "__main__":
    sys.exit(main())
