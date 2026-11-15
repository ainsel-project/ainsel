#!/usr/bin/env python3
"""Tests for scripts/check-conventional-commits.py - the changelog-source gate.

Run: python3 -m unittest scripts.test_check_conventional_commits  (or python3 scripts/test_check_conventional_commits.py)

With `include-pull-requests: false` the failure mode this gate exists for is a
silent one: a non-conventional commit merges, ships, and never appears in
CHANGELOG.md. So the tests pin both directions — every subject that
release-please parses has to pass here, and every one it drops has to fail — plus
the exemptions (merge commits, an empty PR) that must not block a clean branch.
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

_MODULE_PATH = Path(__file__).with_name("check-conventional-commits.py")
_spec = importlib.util.spec_from_loader(
    "check_conventional_commits",
    loader=importlib.machinery.SourceFileLoader("check_conventional_commits", str(_MODULE_PATH)),
)
cc = importlib.util.module_from_spec(_spec)
sys.modules["check_conventional_commits"] = cc
_spec.loader.exec_module(cc)


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
            code = cc.main([])
    return code, out.getvalue(), err.getvalue()


class TestSubjects(unittest.TestCase):
    def test_accepts_types_and_scopes(self):
        for subject in (
            "feat: add persona endpoints",
            "fix(hub): stop dropping the correlation id",
            "chore(deps): bump github.com/spf13/cobra from 1.8.0 to 1.9.1",
            "refactor(api)!: remove Agent.spec.enabledMCPs",
            "perf(frontend): stop re-rendering the ledger on poll",
            "revert: roll back the storage-class default",
        ):
            with self.subTest(subject=subject):
                self.assertTrue(cc.is_conventional(commit("a" * 40, subject)))

    def test_rejects_what_release_please_drops(self):
        for subject in (
            "wip: something",
            "Fix login page",  # capitalised type
            "feat:missing space",
            "feat(): empty scope is not a scope",
            "feat: ",  # empty description
            "M1: skills move onto the agent",
            "update docs",
            "",
        ):
            with self.subTest(subject=subject):
                self.assertFalse(cc.is_conventional(commit("a" * 40, subject)))

    def test_only_the_subject_is_judged(self):
        # A body that quotes a conventional subject does not rescue a bad one.
        self.assertFalse(
            cc.is_conventional(commit("a" * 40, "misc tidy\n\nfeat(x): quoted subject"))
        )

    def test_allows_githubs_revert_subject(self):
        # Same call as pr-title.yml: blocking GitHub's own revert PRs is worse
        # than the changelog gap.
        self.assertTrue(cc.is_conventional(commit("a" * 40, 'Revert "feat: add personas"')))


class TestMain(unittest.TestCase):
    def test_passes_a_clean_branch(self):
        text = json.dumps(
            [
                commit("a" * 40, "feat(api): add persona endpoints\n\nSigned-off-by: A <a@e>"),
                commit("b" * 40, "fix(hub): retry on 409"),
            ]
        )
        code, out, err = run(text)
        self.assertEqual(code, 0)
        self.assertIn("2 commits are conventional", out)

    def test_fails_on_one_bad_commit_and_names_it(self):
        text = json.dumps(
            [commit("a" * 40, "fix(hub): retry on 409"), commit("c" * 40, "wip: tidy")]
        )
        code, out, err = run(text)
        self.assertEqual(code, 1)
        self.assertIn("1 of 2 commits", err)
        self.assertIn("wip: tidy", err)
        self.assertNotIn("fix(hub): retry on 409", err)

    def test_skips_merge_commits(self):
        # A branch that merged main in carries GitHub's merge subject into the
        # PR list; it is not a subject the author chose.
        text = json.dumps(
            [
                commit("a" * 40, "fix(hub): retry on 409"),
                commit("m" * 40, "Merge pull request #12 from ainsel-project/main-sync", parents=2),
            ]
        )
        code, out, err = run(text)
        self.assertEqual(code, 0)
        self.assertIn("1 merge commit skipped", out)

    def test_empty_pr_passes(self):
        code, out, err = run("[]")
        self.assertEqual(code, 0)

    def test_paginated_pages_are_flattened(self):
        # gh api --paginate emits one JSON array per page, back to back.
        text = json.dumps([commit("a" * 40, "feat: one")]) + "\n" + json.dumps(
            [commit("b" * 40, "nope")]
        )
        code, out, err = run(text)
        self.assertEqual(code, 1)
        self.assertIn("1 of 2 commits", err)

    def test_malformed_json_exits_two(self):
        code, out, err = run("{not json")
        self.assertEqual(code, 2)
        self.assertIn("could not read the commit list", err)

    def test_notes_a_non_bumping_type(self):
        code, out, err = run(json.dumps([commit("a" * 40, "docs: reword the merge rule")]))
        self.assertEqual(code, 0)
        self.assertIn("does not bump the version", out)


class TestPatternMatchesTheTitleGate(unittest.TestCase):
    def test_same_pattern_as_pr_title_yml(self):
        # pr-title.yml checks the PR title with this regex; the two checks have
        # to agree or a commit can pass one and be dropped by release-please.
        import re
        from pathlib import Path

        workflow = Path(__file__).resolve().parent.parent / ".github" / "workflows" / "pr-title.yml"
        text = workflow.read_text()
        line = next(l for l in text.splitlines() if l.strip().startswith("pattern="))
        pattern = line.strip().split("pattern=", 1)[1].strip().strip("'")
        self.assertEqual(pattern, cc.CONVENTIONAL.pattern)


if __name__ == "__main__":
    unittest.main()
