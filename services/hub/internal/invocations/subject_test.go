package invocations

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// forgejoIssuesOpened is shaped like a real Forgejo `issues` webhook: the
// issue sits at the top level, the repo full name under "repository".
const forgejoIssuesOpened = `{
  "action": "opened",
  "repository": {"id": 7, "full_name": "apps/app", "html_url": "https://git.example/apps/app"},
  "sender": {"login": "dpinsel"},
  "issue": {
    "number": 412,
    "title": "Login fails with a trailing slash",
    "body": "Repro: open /login/ and get a 500.",
    "html_url": "https://git.example/apps/app/issues/412",
    "state": "open"
  }
}`

// forgejoIssueCommentOnPR is shaped like a Forgejo `issue_comment` webhook on a
// pull: the comment carries no pull of its own, the nested issue says it is a
// pull both by nesting a pull_request object and by the top-level is_pull flag.
const forgejoIssueCommentOnPR = `{
  "action": "created",
  "repository": {"full_name": "apps/app"},
  "comment": {"id": 99, "body": "merged, thanks"},
  "is_pull": true,
  "issue": {
    "number": 88,
    "title": "Add SSO login",
    "body": "Implements OIDC.",
    "html_url": "https://git.example/apps/app/pulls/88",
    "pull_request": {"merged": false, "html_url": "https://git.example/apps/app/pulls/88"}
  }
}`

// githubPullRequest is shaped like a real GitHub `pull_request` webhook: the
// pull sits under "pull_request" with its head ref.
const githubPullRequest = `{
  "action": "opened",
  "repository": {"full_name": "dpinsel/website"},
  "pull_request": {
    "number": 7,
    "title": "Redesign the landing page",
    "body": "New hero section.",
    "html_url": "https://github.com/dpinsel/website/pull/7",
    "head": {"ref": "feat/hero", "sha": "deadbeef"},
    "base": {"ref": "main"}
  }
}`

// githubPush is a `push` payload: no issue, no pull, so there is no subject.
const githubPush = `{
  "ref": "refs/heads/main",
  "repository": {"full_name": "apps/app"},
  "commits": [{"id": "deadbeef", "message": "tweak"}]
}`

func TestSubjectFromPayload_IssuesOpened(t *testing.T) {
	got := SubjectFromPayload([]byte(forgejoIssuesOpened))
	if got == nil {
		t.Fatal("expected a subject for an issues payload, got nil")
	}
	if got.Kind != "issue" {
		t.Errorf("Kind = %q, want issue", got.Kind)
	}
	if got.Number != 412 {
		t.Errorf("Number = %d, want 412", got.Number)
	}
	if got.Repo != "apps/app" {
		t.Errorf("Repo = %q, want apps/app", got.Repo)
	}
	if got.Title != "Login fails with a trailing slash" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.URL != "https://git.example/apps/app/issues/412" {
		t.Errorf("URL = %q", got.URL)
	}
	if got.Body == "" {
		t.Error("Body is empty, want the issue body")
	}
	if got.Branch != "" {
		t.Errorf("Branch = %q, want empty for an issue", got.Branch)
	}
}

func TestSubjectFromPayload_IssueCommentOnPull(t *testing.T) {
	got := SubjectFromPayload([]byte(forgejoIssueCommentOnPR))
	if got == nil {
		t.Fatal("expected a subject for an issue_comment on a pull, got nil")
	}
	if got.Kind != "pull" {
		t.Errorf("Kind = %q, want pull", got.Kind)
	}
	if got.Number != 88 {
		t.Errorf("Number = %d, want 88", got.Number)
	}
	if got.Repo != "apps/app" {
		t.Errorf("Repo = %q, want apps/app", got.Repo)
	}
	if got.Title != "Add SSO login" {
		t.Errorf("Title = %q", got.Title)
	}
}

func TestSubjectFromPayload_GithubPullRequest(t *testing.T) {
	got := SubjectFromPayload([]byte(githubPullRequest))
	if got == nil {
		t.Fatal("expected a subject for a pull_request payload, got nil")
	}
	if got.Kind != "pull" {
		t.Errorf("Kind = %q, want pull", got.Kind)
	}
	if got.Number != 7 {
		t.Errorf("Number = %d, want 7", got.Number)
	}
	if got.Repo != "dpinsel/website" {
		t.Errorf("Repo = %q, want dpinsel/website", got.Repo)
	}
	if got.Branch != "feat/hero" {
		t.Errorf("Branch = %q, want feat/hero (pull_request.head.ref)", got.Branch)
	}
	if got.URL != "https://github.com/dpinsel/website/pull/7" {
		t.Errorf("URL = %q", got.URL)
	}
}

func TestSubjectFromPayload_PushHasNoSubject(t *testing.T) {
	if got := SubjectFromPayload([]byte(githubPush)); got != nil {
		t.Fatalf("expected nil for a push payload, got %+v", got)
	}
}

// TestSubjectFromPayload_TruncatesLongBodyOnRuneBoundary guards the size bound
// on the stored row: a huge issue body is cut at 4096 bytes, and the cut never
// splits a multi-byte rune — invalid UTF-8 would break every JSON reader.
func TestSubjectFromPayload_TruncatesLongBodyOnRuneBoundary(t *testing.T) {
	// 1000 runes of "ä" (2 bytes) + 1000 runes of "€" (3 bytes) = 5000 bytes,
	// with byte 4096 landing inside a 3-byte rune so the cut has to back off.
	long := strings.Repeat("ä", 1000) + strings.Repeat("€", 1000)
	payload, err := json.Marshal(map[string]any{
		"repository": map[string]any{"full_name": "apps/app"},
		"issue": map[string]any{
			"number": 1,
			"title":  "long body",
			"body":   long,
		},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if len([]byte(long)) <= 4096 {
		t.Fatalf("fixture body is only %d bytes, want > 4096", len([]byte(long)))
	}

	got := SubjectFromPayload(payload)
	if got == nil {
		t.Fatal("expected a subject, got nil")
	}
	if len([]byte(got.Body)) > 4096 {
		t.Errorf("Body is %d bytes, want at most 4096", len([]byte(got.Body)))
	}
	if !utf8.ValidString(got.Body) {
		t.Errorf("Body is not valid UTF-8 after truncation: last 8 bytes %v", []byte(got.Body)[max(0, len(got.Body)-8):])
	}
	if !strings.HasPrefix(long, got.Body) {
		t.Error("truncated Body is not a prefix of the original body")
	}
}

func TestSubjectFromPayload_NilCases(t *testing.T) {
	cases := map[string]string{
		"empty":         ``,
		"not json":      `not json at all`,
		"no number":     `{"repository":{"full_name":"apps/app"},"issue":{"title":"no number"}}`,
		"no repository": `{"issue":{"number":3,"title":"x"}}`,
		"neither issue": `{"repository":{"full_name":"apps/app"},"action":"opened"}`,
	}
	for name, raw := range cases {
		if got := SubjectFromPayload([]byte(raw)); got != nil {
			t.Errorf("[%s] expected nil, got %+v", name, got)
		}
	}
}

// TestSubjectFromPayload_NullPullRequestIsAnIssue covers payloads that always
// carry an "issue" object with a null "pull_request" (a plain issue comment):
// a null is not a non-null object, so the subject stays an issue.
func TestSubjectFromPayload_NullPullRequestIsAnIssue(t *testing.T) {
	got := SubjectFromPayload([]byte(`{"repository":{"full_name":"apps/app"},"issue":{"number":3,"title":"x","pull_request":null}}`))
	if got == nil {
		t.Fatal("expected a subject, got nil")
	}
	if got.Kind != "issue" {
		t.Errorf("Kind = %q, want issue", got.Kind)
	}
}
