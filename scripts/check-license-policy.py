#!/usr/bin/env python3
"""Fail if the committed third-party manifest holds a license this project may not ship.

AInsel is Apache-2.0 and its artifacts redistribute dependencies: the frontend SPA
bundle, the Go service and operator images, the pi runtime image. A copyleft
dependency arriving through a routine version bump would be shipped without
anyone reading it. This is the guard.

Why this reads THIRD_PARTY_LICENSES.md rather than running google/go-licenses:
go-licenses v1.6.0 cannot load a module whose go.mod declares go >= 1.25
(https://github.com/google/go-licenses/issues/128) and every module here declares
1.26, so it errors on std packages before it classifies anything. The generator
that produced the manifest (`scripts/generate-third-party-licenses.py`) resolves
the same question with `go list -deps` plus a license-text classifier, over all
three distribution surfaces — including the npm trees go-licenses never sees. So
the manifest is the source of truth and this is the policy over it.

It is also what ships: the manifest and LICENSES/ are the attribution bundle the
images carry, and ci-licenses.yml fails when they drift from the lockfiles.
Checking the committed file therefore checks exactly what a user receives.

Run from the repository root:

    python3 scripts/check-license-policy.py
    python3 scripts/check-license-policy.py --file path/to/THIRD_PARTY_LICENSES.md

Exit 0 = every row allowed (warnings do not fail), 1 = a disallowed license,
2 = the manifest could not be read or held no rows.
"""

from __future__ import annotations

import argparse
import collections
import re
import sys
from pathlib import Path

# Licenses that carry no obligation beyond keeping the notice and this manifest
# in the distribution. Anything not listed here and not listed below is a
# violation: the default is deny, so a new license has to be *decided*, not
# assumed.
ALLOWED = {
    "MIT",
    "Apache-2.0",
    "BSD-2-Clause",
    "BSD-3-Clause",
    "ISC",
    "0BSD",
    "Unlicense",
    "CC0-1.0",
    "BlueOak-1.0.0",
    "Unicode-DFS-2016",
}

# Weak copyleft already in the shipped tree, kept deliberately. Adding an entry
# here is a licence-policy change: it needs a maintainer's review and a reason,
# so it stays a one-line diff with an argument attached rather than a silent
# widening. What each entry obliges is written next to it.
REVIEWED_EXCEPTIONS = {
    # File-level copyleft: obligations attach to the MPL-covered *files*, not to
    # the work as a whole, so an Apache-2.0 project may link them. Source of those
    # files must remain available — it is, these are public Go modules built into
    # the images from upstream. (hashicorp/go-cleanhttp, hashicorp/go-retryablehttp,
    # letsencrypt/boulder.)
    "MPL-2.0": "file-level copyleft; obligations do not extend to our code",
    # Same posture as MPL-2.0 with a broader patent grant. Arrives through
    # elkjs, which mermaid needs for layout. (elkjs.)
    "EPL-2.0": "weak copyleft with an optional secondary-licensing grant",
}

# A manifest row: | `name` | version | license | links |
ROW_RE = re.compile(r"^\| `(?P<name>[^`]+)` \| (?P<version>[^|]*) \| (?P<license>[^|]*) \|")


def parse_rows(text: str) -> list[dict]:
    """Every manifest row, with the `##` section it sits in.

    Failing loudly is the point of counting rows here: a renderer change that
    renames a column would otherwise look like "0 rows, all fine".
    """
    rows: list[dict] = []
    section = ""
    for line in text.splitlines():
        if line.startswith("## "):
            section = line[3:].strip()
            continue
        match = ROW_RE.match(line)
        if match:
            rows.append(
                {
                    "section": section,
                    "name": match.group("name"),
                    "version": match.group("version").strip(),
                    "license": match.group("license").strip(),
                }
            )
    return rows


def expression(license_cell: str) -> tuple[list[str], str]:
    """The license ids of a cell, and how to read them: 'any' or 'all'.

    Three shapes come out of the generator:

      `MIT`                                            -> (["MIT"], "all")
      `Apache-2.0 + MIT`                               -> two texts in one package;
                                                         both must be satisfied
      `(MPL-2.0 OR Apache-2.0) — Apache-2.0 elected`    -> dual-licensed; the
                                                         election in NOTICE picks
                                                         the one we ship under
    """
    cell = license_cell.strip()
    elected = re.search(r"[—-]\s*([A-Za-z0-9.+-]+)\s+elected", cell)
    if elected:
        return [elected.group(1)], "any"
    if " OR " in cell:
        parts = [p.strip("() ") for p in cell.split(" OR ")]
        return [p for p in parts if p], "any"
    return [p.strip("() ") for p in cell.split("+") if p.strip()], "all"


def evaluate(license_cell: str) -> tuple[str, str, list[str]]:
    """(verdict, detail, ids) for one license cell.

    verdict is 'ok', 'exception', 'unknown' (a warning) or 'violation'.
    """
    ids, mode = expression(license_cell)
    if not ids:
        return "violation", "empty license cell", []

    allowed = [i for i in ids if i in ALLOWED]
    excepted = [i for i in ids if i in REVIEWED_EXCEPTIONS]
    unknown = [i for i in ids if i == "Unknown"]
    disallowed = [i for i in ids if i not in ALLOWED and i not in REVIEWED_EXCEPTIONS and i != "Unknown"]

    if mode == "any":
        # A dual license is usable under whichever option we elect, so one
        # acceptable option is enough.
        if allowed:
            return "ok", "elected/dual option is allowed", ids
        if excepted:
            return "exception", "; ".join(REVIEWED_EXCEPTIONS[i] for i in excepted), ids
        if unknown:
            return "violation", "no license identified — nothing permitted to check", ids
        return "violation", "no permitted option", ids

    # 'all': every license in the package applies, so every one must be clear.
    if disallowed:
        return "violation", "applies alongside an allowed license but is not permitted", ids
    if unknown and not allowed and not excepted:
        # Not a warning: the package declares no license and its license text did
        # not classify, so what we would be redistributing is unknown. Shipping
        # that silently is the failure this check exists for.
        return "violation", "no license identified — the package declares none and its text did not classify", ids
    if excepted:
        return "exception", "; ".join(REVIEWED_EXCEPTIONS[i] for i in excepted), ids
    if unknown:
        # The package's own license is identified and allowed; the Unknown is a
        # second text file in it that the classifier could not name. Worth
        # printing, not worth blocking a release on.
        return "unknown", "secondary license text unclassified", ids
    return "ok", "", ids


def check(rows: list[dict]) -> tuple[list[str], list[str], collections.Counter]:
    """Rows -> (violations, warnings, license histogram)."""
    violations: list[str] = []
    warnings: list[str] = []
    histogram: collections.Counter = collections.Counter()
    for row in rows:
        verdict, detail, ids = evaluate(row["license"])
        for i in ids:
            histogram[i] += 1
        where = f"{row['name']}@{row['version']}"
        if verdict == "violation":
            violations.append(f"  {where} — {row['license']}  [{row['section']}]\n    {detail}")
        elif verdict == "unknown":
            warnings.append(f"  {where} — {row['license']}: {detail}")
    return violations, warnings, histogram


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument(
        "--file",
        type=Path,
        default=Path(__file__).resolve().parent.parent / "THIRD_PARTY_LICENSES.md",
        help="manifest to check (default: the committed one)",
    )
    args = parser.parse_args(argv)

    try:
        text = args.file.read_text(encoding="utf-8")
    except OSError as exc:
        print(f"error: cannot read {args.file}: {exc.strerror}", file=sys.stderr)
        return 2

    rows = parse_rows(text)
    if not rows:
        # A manifest with no rows means the parser and the renderer disagree —
        # not that there are no dependencies. Never pass on that.
        print(f"error: no dependency rows found in {args.file}", file=sys.stderr)
        print("  the row format changed; update ROW_RE in this script.", file=sys.stderr)
        return 2

    violations, warnings, histogram = check(rows)

    for license_id, count in sorted(histogram.items()):
        mark = (
            "allowed"
            if license_id in ALLOWED
            else "EXCEPTION"
            if license_id in REVIEWED_EXCEPTIONS
            else "unclassified"
            if license_id == "Unknown"
            else "DISALLOWED"
        )
        print(f"  {license_id:<28} {count:>4}  {mark}")
    print(f"{len(rows)} dependency rows checked")

    if warnings:
        print(f"\nwarnings ({len(warnings)}): unclassified license text, not blocking")
        for line in warnings:
            print(line)

    if not violations:
        exceptions = [i for i in histogram if i in REVIEWED_EXCEPTIONS]
        if exceptions:
            print(
                "\nDependencies ship under a reviewed copyleft exception: "
                + ", ".join(sorted(exceptions))
                + " — see REVIEWED_EXCEPTIONS for what each obliges."
            )
        print("license policy: ok")
        return 0

    print(f"\nviolations ({len(violations)}): not permitted for redistribution", file=sys.stderr)
    for line in violations:
        print(line, file=sys.stderr)
    print(
        f"""
A dependency whose license is not permitted cannot ship in an Apache-2.0 product
without a decision about what we are willing to distribute and under what terms.

To resolve it:

  * Prefer a replacement: another package under MIT/Apache-2.0/BSD/ISC doing the
    same job is always the cheaper answer.
  * If the license is genuinely acceptable, add it to REVIEWED_EXCEPTIONS in
    scripts/check-license-policy.py *with the reason it is acceptable* — what it
    obliges, and why that is fine here. That is a licence-policy change and needs
    a maintainer's review; an exception without a reason is how a policy rots.
  * Never edit THIRD_PARTY_LICENSES.md by hand to silence this check. It is
    generated, and ci-licenses.yml regenerates it and fails on any diff.""",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
