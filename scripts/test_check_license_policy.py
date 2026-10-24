#!/usr/bin/env python3
"""Tests for scripts/check-license-policy.py - the dependency licence gate.

Run: python3 -m unittest scripts.test_check_license_policy  (or python3 scripts/test_check_license_policy.py)

A deny-by-default list has two ways to be wrong in opposite directions: too tight
and it blocks a release over a license that was fine, too loose and it reports
"ok" on something we may not redistribute. So the tests pin both edges — the
shapes the generator emits (single, `+`, `OR`, an election), what an unclassified
license means, and that an unreadable manifest is an error rather than a pass.
"""

import contextlib
import importlib.machinery
import importlib.util
import io
import sys
import tempfile
import unittest
from pathlib import Path

_MODULE_PATH = Path(__file__).with_name("check-license-policy.py")
_spec = importlib.util.spec_from_loader(
    "check_license_policy",
    loader=importlib.machinery.SourceFileLoader("check_license_policy", str(_MODULE_PATH)),
)
clp = importlib.util.module_from_spec(_spec)
sys.modules["check_license_policy"] = clp
_spec.loader.exec_module(clp)

REPO_ROOT = Path(__file__).resolve().parent.parent


def manifest(rows, section="Go modules"):
    """A manifest in the shape render_markdown writes, with the given rows."""
    lines = [
        "# Third-Party Licenses",
        "",
        f"## {section}",
        "",
        "| Package | Version | License | License texts |",
        "| --- | --- | --- | --- |",
    ]
    for name, version, license_cell in rows:
        lines.append(f"| `{name}` | {version} | {license_cell} | [LICENSE](LICENSES/x/LICENSE) |")
    return "\n".join(lines) + "\n"


def check_text(text):
    """Run main() over a manifest string; return (code, stdout, stderr)."""
    out, err = io.StringIO(), io.StringIO()
    with tempfile.NamedTemporaryFile("w", suffix=".md", delete=False, encoding="utf-8") as fh:
        fh.write(text)
        path = Path(fh.name)
    try:
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = clp.main(["--file", str(path)])
    finally:
        path.unlink()
    return code, out.getvalue(), err.getvalue()


class TestParseRows(unittest.TestCase):
    def test_reads_rows_with_their_section(self):
        text = manifest([("a", "1.0.0", "MIT")], section="Node.js - frontend")
        rows = clp.parse_rows(text)
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["name"], "a")
        self.assertEqual(rows[0]["section"], "Node.js - frontend")

    def test_ignores_header_and_prose(self):
        text = "| Package | Version | License | License texts |\n| --- | --- | --- | --- |\n"
        self.assertEqual(clp.parse_rows(text), [])


class TestExpression(unittest.TestCase):
    def test_single_license(self):
        self.assertEqual(clp.expression("MIT"), (["MIT"], "all"))

    def test_plus_means_both_apply(self):
        self.assertEqual(clp.expression("Apache-2.0 + MIT"), (["Apache-2.0", "MIT"], "all"))

    def test_or_means_either_option(self):
        self.assertEqual(clp.expression("(MPL-2.0 OR Apache-2.0)"), (["MPL-2.0", "Apache-2.0"], "any"))

    def test_election_narrows_to_the_elected_option(self):
        cell = "(MPL-2.0 OR Apache-2.0) — Apache-2.0 elected (see NOTICE)"
        self.assertEqual(clp.expression(cell), (["Apache-2.0"], "any"))


class TestEvaluate(unittest.TestCase):
    def test_allowed(self):
        self.assertEqual(clp.evaluate("BSD-3-Clause")[0], "ok")

    def test_copyleft_is_a_violation(self):
        self.assertEqual(clp.evaluate("GPL-3.0")[0], "violation")
        self.assertEqual(clp.evaluate("AGPL-3.0")[0], "violation")
        self.assertEqual(clp.evaluate("LGPL-2.1")[0], "violation")

    def test_dual_license_needs_one_permitted_option(self):
        self.assertEqual(clp.evaluate("(MPL-2.0 OR Apache-2.0)")[0], "ok")

    def test_elected_license_decides_a_dual_license(self):
        # EPL-2.0 alone is an exception; elected Apache-2.0 it needs none.
        self.assertEqual(clp.evaluate("(EPL-2.0 OR Apache-2.0) — Apache-2.0 elected")[0], "ok")

    def test_compound_licenses_all_must_be_clear(self):
        self.assertEqual(clp.evaluate("Apache-2.0 + MIT")[0], "ok")
        self.assertEqual(clp.evaluate("Apache-2.0 + GPL-3.0")[0], "violation")

    def test_unclassified_text_beside_an_allowed_license_warns(self):
        # The package's own license is known and permitted; a second text file in
        # it that the classifier could not name is reported, not fatal.
        self.assertEqual(clp.evaluate("Apache-2.0 + Unknown")[0], "unknown")

    def test_unidentified_license_is_a_violation(self):
        # Nothing declared and nothing classified: what we would redistribute is
        # unknown, which is the case the gate exists for.
        self.assertEqual(clp.evaluate("Unknown")[0], "violation")

    def test_reviewed_exceptions_pass(self):
        self.assertEqual(clp.evaluate("MPL-2.0")[0], "exception")
        self.assertEqual(clp.evaluate("EPL-2.0")[0], "exception")

    def test_empty_cell_is_a_violation(self):
        self.assertEqual(clp.evaluate("")[0], "violation")


class TestMain(unittest.TestCase):
    def test_allowed_manifest_passes(self):
        code, out, _ = check_text(manifest([("a", "1.0.0", "MIT"), ("b", "2.0.0", "ISC")]))
        self.assertEqual(code, 0)
        self.assertIn("2 dependency rows checked", out)
        self.assertIn("license policy: ok", out)

    def test_violation_names_the_package_and_license(self):
        # Acceptance criterion: the message has to say which package and which
        # license, or the PR author has to go hunting through 567 rows.
        code, _, err = check_text(manifest([("a", "1.0.0", "MIT"), ("evil", "9.9.9", "GPL-3.0")]))
        self.assertEqual(code, 1)
        self.assertIn("evil@9.9.9 — GPL-3.0", err)
        self.assertIn("Go modules", err)
        self.assertIn("REVIEWED_EXCEPTIONS", err)

    def test_unclassified_secondary_license_warns_without_failing(self):
        code, out, _ = check_text(manifest([("a", "1.0.0", "Apache-2.0 + Unknown")]))
        self.assertEqual(code, 0)
        self.assertIn("warnings (1)", out)

    def test_reviewed_exception_is_reported_on_success(self):
        # Passing quietly would hide that copyleft ships; the summary says so.
        code, out, _ = check_text(manifest([("a", "1.0.0", "MPL-2.0")]))
        self.assertEqual(code, 0)
        self.assertIn("reviewed copyleft exception: MPL-2.0", out)

    def test_manifest_without_rows_is_an_error(self):
        # Zero rows means the parser and the renderer disagree, not that there is
        # nothing to check. Passing here would be the silent-green failure.
        code, _, err = check_text("# Third-Party Licenses\n\nNothing here.\n")
        self.assertEqual(code, 2)
        self.assertIn("no dependency rows found", err)

    def test_unreadable_manifest_is_an_error(self):
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = clp.main(["--file", str(REPO_ROOT / "does-not-exist.md")])
        self.assertEqual(code, 2)
        self.assertIn("cannot read", err.getvalue())

    def test_committed_manifest_passes(self):
        # The acceptance criterion that matters: today's tree is clean, so this
        # check can be turned on without a red pipeline.
        out, err = io.StringIO(), io.StringIO()
        with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
            code = clp.main([])
        self.assertEqual(code, 0, msg=err.getvalue())

    def test_every_exception_carries_a_reason(self):
        # The list is only reviewable if each entry argues for itself.
        for license_id, reason in clp.REVIEWED_EXCEPTIONS.items():
            self.assertTrue(reason.strip(), f"{license_id} has no reason")


if __name__ == "__main__":
    unittest.main()
