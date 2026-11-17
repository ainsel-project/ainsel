#!/usr/bin/env python3
"""Tests for scripts/check-filter-ops.py - the filter-operator docs-sync gate.

Run: python3 -m unittest scripts.test_check_filter_ops  (or python3 scripts/test_check_filter_ops.py)

The gate exists because the `contains not` / `not in` aliases worked at runtime
while every doc table omitted them (issue #351). The tests pin both directions:
a doc that drops an op or alias must fail, and a consistent tree must pass.
"""

import importlib.machinery
import importlib.util
import io
import shutil
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

_MODULE_PATH = Path(__file__).with_name("check-filter-ops.py")
_spec = importlib.util.spec_from_loader(
    "check_filter_ops",
    loader=importlib.machinery.SourceFileLoader("check_filter_ops", str(_MODULE_PATH)),
)
fo = importlib.util.module_from_spec(_spec)
sys.modules["check_filter_ops"] = fo
_spec.loader.exec_module(fo)

GO_TEMPLATE = '''package ainselapishared

var FilterOps = []string{{
	{ops},
}}

var filterOpAliases = map[string]string{{
{aliases}}}
'''

OPENAPI_TEMPLATE = """openapi: 3.1.0
components:
  schemas:
    Filter:
      type: object
      properties:
        op:
          type: string
          enum: [{enum}]
"""


def make_tree(root: Path, ops: list[str], aliases: dict[str, str],
              docs: dict[str, list[str]], enum: list[str]) -> None:
    (root / "shared" / "api").mkdir(parents=True)
    (root / "openapi").mkdir()
    (root / "docs").mkdir()
    (root / "shared" / "api" / "filter.go").write_text(GO_TEMPLATE.format(
        ops=", ".join(f'"{o}"' for o in ops),
        aliases="".join(f'\t"{a}": "{c}",\n' for a, c in aliases.items()),
    ))
    (root / "openapi" / "hub.yaml").write_text(OPENAPI_TEMPLATE.format(
        enum=", ".join(enum)))
    for doc, tokens in docs.items():
        p = root / doc
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(" ".join(f"`{t}`" for t in tokens))


FULL_OPS = ["eq", "neq", "prefix", "suffix", "contains", "not-contains", "in", "not-in", "regex"]
FULL_ALIASES = {"contains not": "not-contains", "not in": "not-in"}


def full_docs() -> dict[str, list[str]]:
    tokens = FULL_OPS + list(FULL_ALIASES)
    return {d: list(tokens) for d in fo.DOCS}


class CheckFilterOpsTest(unittest.TestCase):
    def run_check(self, root: Path) -> tuple[int, str]:
        # main() resolves the repo root from __file__, so copy the script into
        # the temp tree and load that copy.
        scripts = root / "scripts"
        scripts.mkdir(exist_ok=True)
        shutil.copy(_MODULE_PATH, scripts / "check-filter-ops.py")
        spec = importlib.util.spec_from_loader(
            "cfo_tmp",
            loader=importlib.machinery.SourceFileLoader("cfo_tmp", str(scripts / "check-filter-ops.py")),
        )
        mod = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(mod)
        buf = io.StringIO()
        with redirect_stdout(buf):
            rc = mod.main()
        return rc, buf.getvalue()

    def test_consistent_tree_passes(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            make_tree(root, FULL_OPS, FULL_ALIASES, full_docs(), FULL_OPS)
            rc, out = self.run_check(root)
            self.assertEqual(rc, 0, out)

    def test_doc_missing_alias_fails(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            docs = full_docs()
            docs["docs/crd-reference.md"] = [t for t in docs["docs/crd-reference.md"] if t != "contains not"]
            make_tree(root, FULL_OPS, FULL_ALIASES, docs, FULL_OPS)
            rc, out = self.run_check(root)
            self.assertEqual(rc, 1)
            self.assertIn("crd-reference.md", out)
            self.assertIn("contains not", out)

    def test_doc_missing_op_fails(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            docs = full_docs()
            docs["docs/api-reference.md"] = [t for t in docs["docs/api-reference.md"] if t != "regex"]
            make_tree(root, FULL_OPS, FULL_ALIASES, docs, FULL_OPS)
            rc, out = self.run_check(root)
            self.assertEqual(rc, 1)
            self.assertIn("regex", out)

    def test_openapi_enum_drift_fails(self):
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            make_tree(root, FULL_OPS, FULL_ALIASES, full_docs(), FULL_OPS[:-1])
            rc, out = self.run_check(root)
            self.assertEqual(rc, 1)
            self.assertIn("enum", out)

    def test_parse_helpers(self):
        src = GO_TEMPLATE.format(
            ops='"eq", "regex"', aliases='"not in": "not-in",\n')
        self.assertEqual(fo.parse_canonical_ops(src), ["eq", "regex"])
        self.assertEqual(fo.parse_aliases(src), {"not in": "not-in"})
        self.assertEqual(fo.parse_aliases("package x\n"), {})


if __name__ == "__main__":
    unittest.main()
