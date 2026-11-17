#!/usr/bin/env python3
"""Tests for scripts/check-doc-links.py - the relative-Markdown-link gate.

Run: python3 scripts/test_check_doc_links.py

A link checker fails in two opposite directions: it misses real danglers (a
checker that matches nothing is worse than none, because it is trusted), or it
cries wolf on links that work and gets disabled. So the fixtures pin both
edges: every accepted target shape must pass, a dangling one must fail with
its path and line, and the skip rules (external URLs, anchors, LICENSES/,
code) must skip for the documented reason - not by accident.
"""

import importlib.machinery
import importlib.util
import sys
import tempfile
import unittest
from pathlib import Path

_MODULE_PATH = Path(__file__).with_name("check-doc-links.py")
_spec = importlib.util.spec_from_loader(
    "check_doc_links",
    loader=importlib.machinery.SourceFileLoader("check_doc_links", str(_MODULE_PATH)),
)
cdl = importlib.util.module_from_spec(_spec)
sys.modules["check_doc_links"] = cdl
_spec.loader.exec_module(cdl)


class FixtureMixin:
    def setUp(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.root = Path(self._tmp.name)
        self.addCleanup(self._tmp.cleanup)

    def write(self, rel: str, text: str) -> Path:
        path = self.root / rel
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text, encoding="utf-8")
        return path


class TestBrokenLinks(FixtureMixin, unittest.TestCase):
    def test_dangling_target_fails_with_path_line_and_tried_shapes(self):
        self.write("docs/real.md", "ok\n")
        self.write("README.md", "intro\n\nSee [real](docs/missing.md).\n")
        broken = cdl.find_broken_links(self.root)
        self.assertEqual(len(broken), 1)
        self.assertIn("README.md:3", broken[0])
        self.assertIn("docs/missing.md", broken[0])
        # All three resolution shapes are reported, so the fix is obvious.
        self.assertIn("docs/missing.md/README.md", broken[0])
        self.assertIn("docs/missing.md.md", broken[0])

    def test_exact_path_resolves(self):
        self.write("docs/real.md", "ok\n")
        self.write("README.md", "[ok](docs/real.md)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_directory_link_resolves_via_readme(self):
        self.write("services/mcp/README.md", "ok\n")
        self.write("docs/x.md", "[mcp](../services/mcp/)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_bare_directory_link_resolves(self):
        self.write("services/mcp/README.md", "ok\n")
        self.write("docs/x.md", "[mcp](../services/mcp)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_extensionless_target_resolves_via_md(self):
        # docsify-style link, as used throughout docs/_sidebar.md.
        self.write("docs/administrator-guide.md", "# guide\n")
        self.write("docs/_sidebar.md", "- [Guide](administrator-guide)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_anchor_suffix_is_stripped_before_resolving(self):
        self.write("docs/guide.md", "### Plugins\n")
        self.write("chart/docs/cfg.md", "[plugins](../../docs/guide.md#plugins)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_query_suffix_is_stripped_before_resolving(self):
        self.write("docs/guide.md", "ok\n")
        self.write("x.md", "[g](docs/guide.md?v=2)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_optional_title_is_not_part_of_the_target(self):
        self.write("docs/guide.md", "ok\n")
        self.write("x.md", '[g](docs/guide.md "the guide")\n')
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_parent_traversal_resolves(self):
        self.write("docs/guide.md", "ok\n")
        self.write("chart/docs/cfg.md", "[g](../../docs/guide.md)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])


class TestSkippedTargets(FixtureMixin, unittest.TestCase):
    def test_external_and_mailto_urls_are_not_checked(self):
        self.write(
            "x.md",
            "[a](https://example.com/nope)\n[b](http://x/y)\n[c](mailto:a@b.c)\n"
            "[d](tel:+123)\n",
        )
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_pure_anchor_is_not_checked(self):
        self.write("x.md", "[sec](#section)\n[qs](?v=1)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_root_absolute_target_is_not_checked(self):
        # docsify serves these from the site root, not the file tree.
        self.write("x.md", "[a](/docs/nope.md)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_files_under_licenses_are_not_walked(self):
        # Vendored third-party prose with a dangling relative link inside.
        self.write("LICENSES/foo@1.0.0/LICENSE.md", "see [license](BAR-LICENSE)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_links_into_licenses_from_tracked_docs_still_resolve(self):
        self.write("LICENSES/foo@1.0.0/LICENSE", "text\n")
        self.write("THIRD_PARTY_LICENSES.md", "[foo](LICENSES/foo@1.0.0/LICENSE)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_build_and_vendor_dirs_are_not_walked(self):
        self.write("node_modules/pkg/README.md", "[x](missing.md)\n")
        self.write("dist/README.md", "[x](missing.md)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])


class TestCodeIsNotLinks(FixtureMixin, unittest.TestCase):
    def test_fenced_block_content_is_ignored(self):
        self.write(
            "x.md",
            "text\n\n```markdown\n[bad](does/not/exist.md)\n```\n\ntail\n",
        )
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_tilde_fenced_block_content_is_ignored(self):
        self.write("x.md", "~~~\n[bad](does/not/exist.md)\n~~~\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_inline_code_is_ignored(self):
        # The real-world case: a DNS-1123 regex reads as a link otherwise.
        self.write("x.md", "a DNS-1123 label (`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)\n")
        self.assertEqual(cdl.find_broken_links(self.root), [])

    def test_links_after_a_closed_fence_are_checked_again(self):
        self.write("docs/real.md", "ok\n")
        self.write(
            "x.md",
            "```\n[bad](does/not/exist.md)\n```\n\n[good](docs/real.md)\n"
            "[bad2](also/missing.md)\n",
        )
        broken = cdl.find_broken_links(self.root)
        self.assertEqual(len(broken), 1)
        self.assertIn("also/missing.md", broken[0])
        self.assertIn("x.md:6", broken[0])


class TestLinkExtraction(unittest.TestCase):
    def test_line_numbers_refer_to_the_original_text(self):
        targets = cdl.extract_link_targets("a\n\nsee [x](y.md)\n")
        self.assertEqual(targets, [(3, "y.md")])

    def test_image_targets_are_extracted(self):
        targets = cdl.extract_link_targets("![alt](img/missing.png)\n")
        self.assertEqual(targets, [(1, "img/missing.png")])


class TestIsCheckable(unittest.TestCase):
    def test_checkable_and_skipped_shapes(self):
        self.assertTrue(cdl.is_checkable("docs/x.md"))
        self.assertTrue(cdl.is_checkable("../x/"))
        self.assertTrue(cdl.is_checkable("x#frag"))
        self.assertFalse(cdl.is_checkable(""))
        self.assertFalse(cdl.is_checkable("#frag"))
        self.assertFalse(cdl.is_checkable("/site/x.md"))
        self.assertFalse(cdl.is_checkable("https://x/y"))
        self.assertFalse(cdl.is_checkable("mailto:a@b"))


if __name__ == "__main__":
    unittest.main(verbosity=2)
