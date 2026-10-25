#!/usr/bin/env python3
"""Tests for scripts/check-signed-off.py - the DCO gate.

Run: python3 -m unittest scripts.test_check_signed_off  (or python3 scripts/test_check_signed_off.py)

These pin the two directions a gate like this can fail: a false pass, which
leaves the repo without the assertion it claims to require, and a false failure,
which blocks a correctly signed PR and teaches people to work around the check.
So both the acceptance rules (where a trailer has to sit, what shape it has to
have) and the exemptions (merge commits, empty PRs) are pinned here.
"""

import contextlib
import importlib.machinery
import importlib.util
import io
import json
import sys
import unittest
from pathlib import Path
from unittest import mock

_MODULE_PATH = Path(__file__).with_name("check-signed-off.py")
_spec = importlib.util.spec_from_loader(
    "check_signed_off",
    loader=importlib.machinery.SourceFileLoader("check_signed_off", str(_MODULE_PATH)),
)
cs = importlib.util.module_from_spec(_spec)
sys.modules["check_signed_off"] = cs
_spec.loader.exec_module(cs)


def commit(sha, message, parents=1):
    """One commit in the shape GET /pulls/{n}/commits returns."""
    return {
        "sha": sha,
        "commit": {"message": message},
        "parents": [{"sha": f"p{i}"} for i in range(parents)],
    }


def run(text):
    """Run main() against stdin text; return (exit code, stdout, stderr)."""
    out, err = io.StringIO(), io.StringIO()
    with contextlib.redirect_stdout(out), contextlib.redirect_stderr(err):
        with mock.patch.object(sys, "stdin", io.StringIO(text)):
            code = cs.main([])
    return code, out.getvalue(), err.getvalue()


SIGNED = "fix(hub): stop dropping the correlation id\n\nBecause reasons.\n\nSigned-off-by: Ada Lovelace <ada@example.com>\n"


class TestSignoffState(unittest.TestCase):
    def test_accepts_a_conventional_trailer(self):
        self.assertEqual(cs.signoff_state(SIGNED), "ok")

    def test_accepts_the_key_in_any_case(self):
        # Hand-written trailers drift on case; the assertion is the same one.
        self.assertEqual(cs.signoff_state("fix: x\n\nsigned-off-by: Ada <ada@example.com>"), "ok")

    def test_accepts_a_trailer_alongside_other_footers(self):
        msg = (
            "fix: x\n\nBody.\n\n"
            "Closes #18\n"
            "Co-authored-by: Bob <bob@example.com>\n"
            "Signed-off-by: Ada <ada@example.com>\n"
        )
        self.assertEqual(cs.signoff_state(msg), "ok")

    def test_accepts_a_single_paragraph_message(self):
        # `git commit -s -m 'fix: x'` on a message with no body puts the trailer
        # in the only paragraph there is.
        self.assertEqual(cs.signoff_state("fix: x\nSigned-off-by: Ada <ada@example.com>"), "ok")

    def test_rejects_a_trailer_mentioned_in_prose(self):
        # The reason only the final paragraph is read: a commit that talks
        # about sign-off must not certify itself.
        msg = (
            "docs: explain sign-off\n\n"
            "Every commit now carries 'Signed-off-by: Ada <ada@example.com>'.\n"
            "See CONTRIBUTING.md.\n"
        )
        self.assertEqual(cs.signoff_state(msg), "missing")

    def test_rejects_a_malformed_trailer(self):
        # Reported as malformed, not missing: the author did sign, the shape is
        # wrong, and telling them to sign again wastes their time.
        self.assertEqual(cs.signoff_state("fix: x\n\nSigned-off-by: Ada"), "malformed")
        self.assertEqual(cs.signoff_state("fix: x\n\nSigned-off-by: <not-an-email>"), "malformed")

    def test_rejects_a_trailer_followed_by_prose(self):
        # git puts trailers last; a paragraph after the trailer block is not
        # part of it. Border case by design — the fix is one blank line up.
        self.assertEqual(
            cs.signoff_state("fix: x\n\nSigned-off-by: Ada <ada@example.com>\n\nCloses #1"),
            "missing",
        )


class TestReadCommits(unittest.TestCase):
    def test_flattens_concatenated_pages(self):
        # The endpoint caps a page at 100, so a workflow loops and the pages
        # arrive back to back. A PR is judged as a whole.
        text = json.dumps([commit("a", SIGNED)]) + "\n" + json.dumps([commit("b", "fix: b")])
        self.assertEqual([c["sha"] for c in cs.read_commits(text)], ["a", "b"])

    def test_rejects_a_non_array_payload(self):
        # An API error object is not a commit list; failing loudly beats
        # reporting "ok: 0 commits signed off" for a PR that was never read.
        with self.assertRaises(ValueError):
            cs.read_commits('{"message": "Not Found"}')


class TestMain(unittest.TestCase):
    def test_passes_when_every_commit_is_signed(self):
        code, out, _ = run(json.dumps([commit("a", SIGNED), commit("b", SIGNED)]))
        self.assertEqual(code, 0)
        self.assertIn("2 commits signed off", out)

    def test_passes_an_empty_pr(self):
        code, out, _ = run("[]")
        self.assertEqual(code, 0)
        self.assertIn("0 commits", out)

    def test_skips_merge_commits(self):
        # A merge of main brings in other people's commits, each with its own
        # assertion; it is not the merge author's work to certify.
        code, out, _ = run(
            json.dumps([commit("a", SIGNED), commit("m", "Merge main into fix/x", parents=2)])
        )
        self.assertEqual(code, 0)
        self.assertIn("1 merge commits skipped", out)

    def test_fails_on_a_missing_trailer_and_names_the_commit(self):
        code, _, err = run(json.dumps([commit("a", SIGNED), commit("deadbeef", "fix: unsigned")]))
        self.assertEqual(code, 1)
        self.assertIn("1 of 2 commits", err)
        self.assertIn("deadbee  fix: unsigned", err)
        self.assertIn("git rebase --signoff origin/main", err)

    def test_reports_a_malformed_trailer_separately(self):
        code, _, err = run(json.dumps([commit("a", "fix: x\n\nSigned-off-by: Ada")]))
        self.assertEqual(code, 1)
        self.assertIn("not 'Name <email>'", err)
        self.assertNotIn("missing 'Signed-off-by:'", err)

    def test_a_bad_commit_on_a_later_page_still_fails(self):
        text = json.dumps([commit("a", SIGNED)]) + json.dumps([commit("b", "fix: unsigned")])
        code, _, err = run(text)
        self.assertEqual(code, 1)
        self.assertIn("1 of 2 commits", err)

    def test_unparseable_input_is_an_error_not_a_pass(self):
        code, _, err = run('{"message": "Not Found"}')
        self.assertEqual(code, 2)
        self.assertIn("could not read the commit list", err)


if __name__ == "__main__":
    unittest.main()
