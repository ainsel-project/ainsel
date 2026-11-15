#!/usr/bin/env python3
"""Fail if a pull request carries commits release-please cannot parse.

The changelog and the next version come from the conventional-commit subjects of
the commits that land on `main`. PRs merge as merge commits carrying their branch
commits, and `release-please-config.json` sets `include-pull-requests: false`, so
PR titles are never a fallback source: a branch commit whose subject does not
parse ships, and contributes nothing to `CHANGELOG.md` and no version bump — with
no error anywhere. This check is what makes that impossible to miss.

Reads the JSON of GET /repos/{owner}/{repo}/pulls/{n}/commits on stdin, one or
more pages concatenated (the endpoint caps a page at 100 commits):

    gh api --paginate "repos/$GITHUB_REPOSITORY/pulls/$PR/commits?per_page=100" \
      | python3 scripts/check-conventional-commits.py

The endpoint lists the PR's own commits, so commits already on `main` are not
judged here. Merge commits are skipped: a branch that merged `main` in carries
them into the list, and `Merge pull request #123 from …` is GitHub's subject, not
a conventional one the author chose.

The pattern is the one in `.github/workflows/pr-title.yml`, kept as the single
rule for both checks: whatever reaches `main` — a branch commit, or the squashed
subject of the title — has to parse the same way.
"""

import json
import re
import sys

# The types release-please parses. A scope is optional, `!` marks a breaking
# change, and the description must be non-empty.
CONVENTIONAL = re.compile(
    r"^(feat|fix|docs|style|refactor|perf|test|build|ci|chore|revert)"
    r"(\([a-zA-Z0-9 ._/-]+\))?!?: [^ ].*"
)

# GitHub titles its own revert PRs 'Revert "<subject>"'. Blocking one would be
# worse than the changelog gap, same call as pr-title.yml makes.
REVERT = re.compile(r'^Revert ".*"$')

# Types that parse but do not bump the version — worth a note, not a failure.
QUIET_TYPES = {"chore", "ci", "docs", "test", "style", "build"}


def read_commits(text: str) -> list:
    """Parse one or more concatenated JSON arrays — one API page each.

    Same shape as `check-signed-off.py` reads: `gh api --paginate` emits the
    pages back to back, and a PR is judged as a whole, so they have to be
    flattened before any commit is checked.
    """
    decoder = json.JSONDecoder()
    commits: list = []
    idx = 0
    while True:
        while idx < len(text) and text[idx].isspace():
            idx += 1
        if idx >= len(text):
            return commits
        obj, idx = decoder.raw_decode(text, idx)
        if not isinstance(obj, list):
            raise ValueError("expected an array of commits (the /pulls/{n}/commits response)")
        commits.extend(obj)


def is_merge_commit(commit: dict) -> bool:
    """True for a commit with more than one parent."""
    return len(commit.get("parents") or []) > 1


def subject(commit: dict) -> str:
    return ((commit.get("commit") or {}).get("message") or "").split("\n")[0].strip()


def describe(commit: dict) -> str:
    return f"  {(commit.get('sha') or '?')[:7]}  {subject(commit)}"


def is_conventional(commit: dict) -> bool:
    message = subject(commit)
    return bool(CONVENTIONAL.match(message) or REVERT.match(message))


def main(argv: list) -> int:
    try:
        commits = read_commits(sys.stdin.read())
    except (ValueError, json.JSONDecodeError) as exc:
        print(f"error: could not read the commit list: {exc}", file=sys.stderr)
        return 2

    # An empty array is what the endpoint returns for a PR with no commits —
    # nothing to parse, nothing to release.
    checked = [c for c in commits if not is_merge_commit(c)]
    skipped = len(commits) - len(checked)

    bad = [c for c in checked if not is_conventional(c)]

    if not bad:
        note = (
            f" ({skipped} merge commit{'s' if skipped != 1 else ''} skipped)" if skipped else ""
        )
        print(f"ok: {len(checked)} commits are conventional{note}")
        for commit in checked:
            match = CONVENTIONAL.match(subject(commit))
            if match and match.group(1) in QUIET_TYPES:
                print(f"note: '{match.group(1)}' does not bump the version")
        return 0

    print(
        f"error: {len(bad)} of {len(checked)} commits on this PR are not conventional commits",
        file=sys.stderr,
    )
    for commit in bad:
        print(describe(commit), file=sys.stderr)

    print(
        """
release-please writes CHANGELOG.md and computes the next version from the
conventional-commit subjects on `main`. PR titles are not a fallback source
(`include-pull-requests: false`), so a commit it cannot parse ships without ever
appearing in the release.

Expected:  <type>(<optional scope>): <description>
           feat(api): add persona endpoints
           fix(operator): keep the profile MCP token on collision
           refactor(api)!: remove Agent.spec.enabledMCPs

Types:     feat fix docs style refactor perf test build ci chore revert
Breaking:  append ! to the type, or add a 'BREAKING CHANGE: <what>' footer to
           the commit — a marker in the PR title or body is not read.

Fix it:

  git rebase -i origin/main             # reword the commit that is wrong
  git commit --amend                    # if it is the one on top

Then push. If a commit is pure noise, squash it into the one it belongs to rather
than leaving an unparseable subject on the trunk.""",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
