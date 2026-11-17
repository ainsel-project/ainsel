#!/usr/bin/env python3
"""Fail if a relative Markdown link points at a path that does not exist.

Why this exists: chart/docs/configuration.md pointed its hub.agentMCP row at
docs/agents.md, a file that does not exist, and nothing in CI noticed — a
reader opting into the agent MCP plane got a 404 on the one sentence that is
supposed to tell them what they are turning on. An earlier dangle
(chart/docs/configuration.md -> api.md) slipped through the same gap. This is
the repo-hygiene guard for that class of typo, in the shape of the other
checkers: stdlib only, self-contained, run from the repository root.

What counts as a link: inline Markdown links `[text](target)`, images
included. Links inside fenced code blocks and inline code spans are ignored —
docs quote regexes and example Markdown whose brackets are not links.
Reference-style links (`[text][ref]` plus a `[ref]: target` definition) are
not used in this repo and are not resolved.

What a relative target must resolve to (any one of the three passes):
  1. the exact path                       — docs/agents.md
  2. <target>/README.md (or the dir)      — ../../services/mcp/
  3. <target>.md                          — docsify extensionless links,
                                            e.g. `administrator-guide#plugins`
                                            in docs/_sidebar.md
Rule 3 is what lets docs/_sidebar.md be checked instead of waived: the docsify
site renders extensionless links fine, and the issue that asked for this
checker had to exclude those links as false positives because no rule covered
them. Skipping rule 3 would leave the docs site's whole nav unchecked.

What is skipped, and why:
  * Files under LICENSES/ — vendored third-party license texts; their links
    are not ours to repair. (Links *to* LICENSES/ from the generated
    THIRD_PARTY_LICENSES.md still resolve normally.)
  * node_modules/, dist/, build output, .git.
  * External targets (any `scheme:` — http, https, mailto, …) and
    root-absolute targets (`/foo`): docsify serves those from the site root,
    not the file tree.
  * Pure anchors (`#section`).

Known false-negative by design: `#fragment` and `?query` are stripped before
resolving and heading slugs are *not* validated. GitHub and docsify generate
slugs differently (and the docs site renders Markdown through its own path),
so slug validation would fail links that work — a checker that cries wolf
gets disabled. A renamed heading can therefore silently strand a `#anchor`;
that is accepted, not overlooked.

Run from the repository root:

    python3 scripts/check-doc-links.py
    python3 scripts/check-doc-links.py --root path/to/repo

Exit 0 = every relative link resolves, 1 = at least one dangles (each is
printed as `path:line -> target (tried: ...)`), 2 = the root could not be read.
"""

from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path

# Directories that are not ours to check: vendored trees and build output.
SKIP_DIRS = {"node_modules", "dist", "build", ".git"}

# Third-party license texts live here; their prose is not ours to repair.
SKIP_ROOT_DIRS = {"LICENSES"}

# Inline Markdown link/image: [text](target "optional title"). The target
# itself has no whitespace or ')' (angle-bracket targets are not in use).
LINK_RE = re.compile(r"\[[^\]]*\]\(\s*([^)\s]+)(?:\s+[\"'][^\"']*[\"'])?\s*\)")

# A line that opens/closes a fenced code block (``` or ~~~, up to 3 spaces in).
FENCE_RE = re.compile(r"^ {0,3}(`{3,}|~{3,})")

# An inline code span: `code`, ``code``, etc. Removed before link matching so
# regex examples like `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` are not read as links.
CODE_SPAN_RE = re.compile(r"`+[^`\n]*`+")

SCHEME_RE = re.compile(r"^[A-Za-z][A-Za-z0-9+.\-]*:")


def iter_markdown_files(root: Path):
    """Yield every Markdown file under root, skipping vendored and build trees."""
    for dirpath, dirnames, filenames in os.walk(root):
        rel_dir = Path(dirpath).relative_to(root)
        dirnames[:] = sorted(
            d for d in dirnames
            if d not in SKIP_DIRS and not (rel_dir == Path(".") and d in SKIP_ROOT_DIRS)
        )
        for name in sorted(filenames):
            if name.endswith(".md"):
                yield Path(dirpath) / name


def extract_link_targets(text: str) -> list[tuple[int, str]]:
    """Return (line_number, raw_target) for every inline link outside code.

    Fenced code blocks and inline code spans are stripped first; line numbers
    still refer to the original text.
    """
    found: list[tuple[int, str]] = []
    fence: str | None = None
    for lineno, line in enumerate(text.splitlines(), start=1):
        marker = FENCE_RE.match(line)
        if marker:
            tick = marker.group(1)[0] * 3
            if fence is None:
                fence = tick
            elif line.lstrip().startswith(fence):
                fence = None
            continue
        if fence is not None:
            continue
        for match in LINK_RE.finditer(CODE_SPAN_RE.sub("", line)):
            found.append((lineno, match.group(1)))
    return found


def is_checkable(target: str) -> bool:
    """A target is checked when it is a relative path, not an anchor or URL."""
    if not target or target.startswith("#"):
        return False
    if target.startswith("/"):
        return False  # docsify site-absolute; not a file-tree path
    return not SCHEME_RE.match(target)


def resolve(base_dir: Path, target: str, root: Path) -> tuple[bool, list[str]]:
    """Try the three accepted shapes for a relative target.

    Returns (resolved, tried_paths) where tried_paths are repository-relative
    and in the order attempted — printed verbatim when nothing resolves.
    """
    stem = target.split("#", 1)[0].split("?", 1)[0]
    if not stem:
        return True, []
    tried: list[str] = []
    candidates = [stem, f"{stem.rstrip('/')}/README.md", f"{stem}.md"]
    for candidate in candidates:
        path = (base_dir / candidate).resolve()
        tried.append(os.path.relpath(path, root))
        if path.exists():
            return True, tried
    return False, tried


def find_broken_links(root: Path) -> list[str]:
    """Return one message per dangling relative link, in file/line order."""
    messages: list[str] = []
    for md in iter_markdown_files(root):
        try:
            text = md.read_text(encoding="utf-8", errors="replace")
        except OSError as exc:
            messages.append(f"{md.relative_to(root)}: cannot read ({exc})")
            continue
        rel = md.relative_to(root)
        for lineno, target in extract_link_targets(text):
            if not is_checkable(target):
                continue
            ok, tried = resolve(md.parent, target, root)
            if not ok:
                messages.append(
                    f"{rel}:{lineno} -> {target} (tried: {', '.join(tried)})"
                )
    return messages


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--root",
        type=Path,
        default=Path("."),
        help="repository root to walk (default: current directory)",
    )
    args = parser.parse_args(argv)
    root = args.root.resolve()
    if not root.is_dir():
        print(f"error: {args.root} is not a directory", file=sys.stderr)
        return 2
    broken = find_broken_links(root)
    for message in broken:
        print(message)
    if broken:
        print(f"\n{len(broken)} broken relative link(s) in Markdown files", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
