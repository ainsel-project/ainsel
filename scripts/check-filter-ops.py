#!/usr/bin/env python3
"""Fail if the event-filter operator set drifts between code, docs, and OpenAPI.

The canonical operator list lives in shared/api/filter.go (FilterOps) and the
accepted aliases in filterOpAliases. Four docs tables and the openapi/hub.yaml
Filter schema are supposed to enumerate exactly that set — they drifted before
(issue #351: the `contains not` / `not in` aliases worked at runtime but were
documented nowhere). This check pins all of them to the Go source:

  * every canonical op must appear backticked in each doc;
  * every alias must be mentioned backticked in each doc;
  * the openapi Filter schema's `op` enum must equal FilterOps exactly.

Run from the repository root: python3 scripts/check-filter-ops.py
"""

import re
import sys
from pathlib import Path

GO_SOURCE = "shared/api/filter.go"
OPENAPI = "openapi/hub.yaml"
DOCS = [
    "docs/administrator-guide.md",
    "docs/api-reference.md",
    "docs/crd-reference.md",
    "docs/writing-a-connector.md",
]


def parse_canonical_ops(src: str) -> list[str]:
    m = re.search(r"var FilterOps = \[\]string\{(.*?)\}", src, re.DOTALL)
    if not m:
        raise SystemExit(f"{GO_SOURCE}: could not find `var FilterOps = []string{{...}}`")
    return re.findall(r'"([^"]+)"', m.group(1))


def parse_aliases(src: str) -> dict[str, str]:
    m = re.search(r"var filterOpAliases = map\[string\]string\{(.*?)\}", src, re.DOTALL)
    if not m:
        return {}
    return dict(re.findall(r'"([^"]+)":\s*"([^"]+)"', m.group(1)))


def parse_openapi_ops(text: str) -> list[str]:
    # Extract the Filter schema block (an indented `    Filter:` entry under
    # components/schemas up to the next sibling entry) without requiring a
    # YAML parser, then read its `op` enum.
    m = re.search(r"^    Filter:\n((?:      .*|\n)*)", text, re.MULTILINE)
    if not m:
        raise SystemExit(f"{OPENAPI}: could not find the Filter schema")
    m = re.search(r"^\s+enum: \[([^\]]*)\]", m.group(1), re.MULTILINE)
    if not m:
        raise SystemExit(f"{OPENAPI}: Filter schema must enumerate `op` (enum)")
    return [item.strip() for item in m.group(1).split(",") if item.strip()]


def main() -> int:
    root = Path(__file__).resolve().parent.parent
    src = (root / GO_SOURCE).read_text()
    ops = parse_canonical_ops(src)
    aliases = parse_aliases(src)

    failed = False

    def fail(msg: str) -> None:
        nonlocal failed
        failed = True
        print(f"FAIL: {msg}")

    openapi_ops = parse_openapi_ops((root / OPENAPI).read_text())
    if openapi_ops != ops:
        fail(f"{OPENAPI}: Filter op enum {openapi_ops} != canonical ops {ops}")

    for doc in DOCS:
        text = (root / doc).read_text()
        for op in ops:
            if f"`{op}`" not in text:
                fail(f"{doc}: canonical op `{op}` not documented")
        for alias in aliases:
            if f"`{alias}`" not in text:
                fail(f"{doc}: accepted alias `{alias}` not documented")

    if failed:
        print(f"\nCanonical operator set (source of truth: {GO_SOURCE}): "
              + ", ".join(ops)
              + (" | aliases: " + ", ".join(f"{a} -> {c}" for a, c in sorted(aliases.items())) if aliases else ""))
        return 1
    print(f"OK: {len(ops)} operators + {len(aliases)} aliases consistent across code, docs, and OpenAPI")
    return 0


if __name__ == "__main__":
    sys.exit(main())
