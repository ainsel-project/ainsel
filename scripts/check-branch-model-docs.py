#!/usr/bin/env python3
"""Fail if any tracked markdown file still describes the retired develop/main model.

The repository is trunk-based: `main` is the default branch, the integration
line and the release line, and `develop` no longer exists on the remote (PR #69).
Docs that still tell a contributor or an agent to branch from or open a PR
against `develop` misroute their work to a branch that cannot be fetched, and
this already drifted once (issue #73).

The check is deliberately a word-boundary match on `develop`, not a denylist of
phrases: it also catches prose that reworded the stale rule ("cut from
develop", "base on develop", a dead `blob/develop` link) while leaving the word
in place. It does not match `development`, `developer` or `developed`.

One exception exists: `develop-on-assign` is the name of an example trigger in
the event-schema docs, not a branch. Do not "fix" those occurrences.

Historical files are excluded: `LICENSES/**` and any `CHANGELOG.md` record what
was true when they were written and are not ours to rewrite.

Run from the repository root: python3 scripts/check-branch-model-docs.py
Exit 0 means every tracked `.md` is trunk-based; exit 1 lists the stale lines as
`path:line: text` — the same shape as scripts/test-dev-image-workflows.py.
"""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

# Example trigger name in docs/event-schema.md and friends — not a branch.
ALLOWED_TOKENS = ("develop-on-assign",)

# Historical records: excluded from the sweep.
EXCLUDED_DIRS = ("LICENSES/",)
EXCLUDED_NAMES = ("CHANGELOG.md",)

PATTERN = re.compile(r"\bdevelop\b")


def tracked_markdown_files() -> list[str]:
    out = subprocess.run(
        ["git", "ls-files", "-z", "--", "*.md"],
        check=True,
        stdout=subprocess.PIPE,
    ).stdout
    return [p.decode() for p in out.split(b"\0") if p]


def is_excluded(path: str) -> bool:
    if path.startswith(EXCLUDED_DIRS):
        return True
    return Path(path).name in EXCLUDED_NAMES


def main() -> int:
    violations: list[str] = []
    for rel in tracked_markdown_files():
        if is_excluded(rel):
            continue
        try:
            text = Path(rel).read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as exc:
            print(f"{rel}:0: unreadable: {exc}")
            violations.append(rel)
            continue
        for lineno, line in enumerate(text.splitlines(), start=1):
            stripped = line
            for token in ALLOWED_TOKENS:
                stripped = stripped.replace(token, "")
            if PATTERN.search(stripped):
                violations.append(f"{rel}:{lineno}: {line.strip()}")

    for violation in violations:
        print(violation)
    if violations:
        print(
            f"\n{len(violations)} line(s) still reference the retired `develop` "
            "branch. The repo is trunk-based: branch from `origin/main` and open "
            "PRs against `main` (see CONTRIBUTING.md).",
            file=sys.stderr,
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
