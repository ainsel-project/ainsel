#!/usr/bin/env python3
"""Fail if a pull request carries commits without a Signed-off-by trailer.

The trailer is the developer's Certificate of Origin assertion: that the person
pushing the change is entitled to submit it under this repo's licence. GitHub's
terms grant a licence for public repos, so nothing enforces that entitlement on
our behalf — the trailer is the record we keep. A DCO app would do this too;
this is the same check with nothing to install and nothing to approve.

Reads the JSON of GET /repos/{owner}/{repo}/pulls/{n}/commits on stdin, one or
more pages concatenated (the endpoint caps a page at 100 commits), and fails if
any commit on the PR lacks the trailer:

    gh api "repos/$GITHUB_REPOSITORY/pulls/$PR/commits?per_page=100" \
      | python3 scripts/check-signed-off.py

Merge commits are skipped. Their content is other people's commits, each with
its own assertion, and a merge of `main` into a feature branch is not the merge
author's work to certify.

Squash-merge means the trailers do not land on `main` — they stay on the PR's
commit list, which is where the record lives. Authors who want the assertion on
the merge commit put a `Signed-off-by:` line in the PR body, which GitHub
appends to the squashed message.
"""

import json
import re
import sys

# A trailer is `Signed-off-by: Name <email>` — the same shape git's
# interpret-trailers writes. The key is matched case-insensitively because
# hand-written trailers drift on case; the value is not, because a name and an
# address are the whole point of the assertion.
TRAILER = re.compile(r"^signed-off-by:\s*(?P<who>[^<>\n]+ <[^<>\s@]+@[^<>\s]+>)\s*$", re.IGNORECASE)
TRAILER_KEY = re.compile(r"^signed-off-by\s*:", re.IGNORECASE)


def read_commits(text: str) -> list:
    """Parse one or more concatenated JSON arrays — one API page each.

    `gh api` and `curl` both emit the pages back to back when a workflow loops
    over `?page=N`, and a PR is checked as a whole, so the pages have to be
    flattened before any commit is judged.
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


def trailer_block(message: str) -> list:
    """The commit message's final paragraph: where git puts trailers.

    Only this block is read, so a message that discusses the trailer (or quotes
    a commit message in its body) cannot pass by mentioning it in prose.
    """
    lines = message.rstrip().split("\n")
    block = []
    for line in reversed(lines):
        if not line.strip():
            break
        block.append(line)
    block.reverse()
    return block


def signoff_state(message: str) -> str:
    """'ok', 'missing', or 'malformed' for one commit message.

    'malformed' is reported separately from 'missing' because the fix differs:
    a malformed trailer usually means a missing address, and telling the author
    to sign a commit that is already signed wastes their time.
    """
    block = trailer_block(message)
    found = False
    for line in block:
        if TRAILER.match(line.strip()):
            return "ok"
        if TRAILER_KEY.match(line.strip()):
            found = True
    return "malformed" if found else "missing"


def is_merge_commit(commit: dict) -> bool:
    """True for a commit with more than one parent.

    The /pulls/{n}/commits response includes `parents`; a branch that merged
    `main` in carries those commits into the PR list.
    """
    return len(commit.get("parents") or []) > 1


def describe(commit: dict) -> str:
    sha = (commit.get("sha") or "?")[:7]
    subject = ((commit.get("commit") or {}).get("message") or "").split("\n")[0].strip()
    return f"  {sha}  {subject}"


def main(argv: list) -> int:
    try:
        commits = read_commits(sys.stdin.read())
    except (ValueError, json.JSONDecodeError) as exc:
        print(f"error: could not read the commit list: {exc}", file=sys.stderr)
        return 2

    # An empty array is what the endpoint returns for a PR with no commits —
    # not an error, and nothing to certify.
    checked = [c for c in commits if not is_merge_commit(c)]
    skipped = len(commits) - len(checked)

    bad = []
    for commit in checked:
        state = signoff_state((commit.get("commit") or {}).get("message") or "")
        if state != "ok":
            bad.append((state, commit))

    if not bad:
        note = f" ({skipped} merge commits skipped)" if skipped else ""
        print(f"ok: {len(checked)} commits signed off{note}")
        return 0

    missing = [c for state, c in bad if state == "missing"]
    malformed = [c for state, c in bad if state == "malformed"]
    print(
        f"error: {len(bad)} of {len(checked)} commits on this PR are not signed off",
        file=sys.stderr,
    )
    if missing:
        print("\n  missing 'Signed-off-by:'", file=sys.stderr)
        for commit in missing:
            print(describe(commit), file=sys.stderr)
    if malformed:
        print(
            "\n  'Signed-off-by:' present but not 'Name <email>' — a name and an address are both required",
            file=sys.stderr,
        )
        for commit in malformed:
            print(describe(commit), file=sys.stderr)

    print(
        """
The trailer is your Certificate of Origin: that you wrote this change or
otherwise have the right to submit it under this project's licence. It goes in
the commit message's last paragraph, on its own line:

    fix: stop dropping the correlation id

    Signed-off-by: Ada Lovelace <ada@example.com>

Fix it:

  git commit --amend --signoff          # the commit you just made
  git rebase --signoff origin/main      # every commit on this branch
  git commit -s ...                     # and every commit from now on

Then push. If a commit is not yours to certify, it should not be on this
branch — rebase it out and let its author submit it.

(Record note: squash-merge does not carry trailers onto main. Add a
'Signed-off-by:' line to the PR body if you want it on the merge commit too.)""",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
