package invocations

import (
	"encoding/json"
	"unicode/utf8"
)

// subjectBodyBytes caps how much of an issue or pull body is kept on the
// invocation row. The subject exists to answer "what is this run about" without
// reading the event payload, so it carries enough of a description to skim and
// no more: the full text stays available on the event itself.
const subjectBodyBytes = 4096

// subjectPayload is the subset of a webhook body that names the issue or pull an
// event is about. GitHub, Forgejo and Gitea all put the repository under
// "repository" and the work item in either a top-level "issue" (issue and
// comment events) or a top-level "pull_request" (pull events), so one shape
// covers every source the hub accepts. Unknown fields are ignored, and a body
// that is not JSON at all simply leaves every field zero — which SubjectFromPayload
// reads as "no subject".
type subjectPayload struct {
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	// IsPull is Forgejo's and Gitea's convenience flag on issue-bearing events
	// (issue_comment in particular), where there is no top-level pull_request.
	IsPull bool `json:"is_pull"`
	// PullRequest is set on pull events, in GitHub's and Forgejo's shape.
	PullRequest *struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Head    struct {
			Ref string `json:"ref"`
		} `json:"head"`
	} `json:"pull_request"`
	// Issue is set on issue and comment events, including the ones about a pull.
	Issue *struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		// PullRequest is a non-null object when the issue is a pull, and null or
		// absent for a plain issue. Raw, because "present but null" has to be
		// told apart from "a nested pull object".
		PullRequest json.RawMessage `json:"pull_request"`
	} `json:"issue"`
}

// SubjectFromPayload extracts the subject from a webhook payload in the GitHub,
// Forgejo or Gitea shape. It is called once per event at dispatch time, so the
// invocation row carries the work item on its own.
//
// A pull wins over an issue when a payload happens to carry both. The kind is
// pull when the payload has a top-level pull_request, when issue.pull_request is
// a non-null object, or when the top-level is_pull flag says so — the three ways
// the forges spell "this comment is on a pull".
//
// It returns nil when the payload names no work item: no JSON, no issue and no
// pull, a zero number, or no repository to qualify the number with.
func SubjectFromPayload(data []byte) *Subject {
	if len(data) == 0 {
		return nil
	}
	var p subjectPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return nil
	}

	s := &Subject{Repo: p.Repository.FullName}
	switch {
	case p.PullRequest != nil:
		s.Kind = SubjectKindPull
		s.Number = p.PullRequest.Number
		s.Title = p.PullRequest.Title
		s.Body = p.PullRequest.Body
		s.URL = p.PullRequest.HTMLURL
		s.Branch = p.PullRequest.Head.Ref
	case p.Issue != nil:
		s.Kind = SubjectKindIssue
		if isNonNullObject(p.Issue.PullRequest) || p.IsPull {
			s.Kind = SubjectKindPull
		}
		s.Number = p.Issue.Number
		s.Title = p.Issue.Title
		s.Body = p.Issue.Body
		s.URL = p.Issue.HTMLURL
	default:
		return nil
	}

	// A number with no repository is not addressable, and a zero number is not
	// either. Either way there is nothing worth storing on the row.
	if s.Number == 0 || s.Repo == "" {
		return nil
	}
	s.Body = truncateBody(s.Body)
	return s
}

// isNonNullObject reports whether a raw JSON field holds an object rather than
// null or a scalar.
func isNonNullObject(raw json.RawMessage) bool {
	for _, c := range raw {
		switch c {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

// truncateBody cuts a body to subjectBodyBytes, backing the cut point off to the
// previous rune start so a multi-byte rune is never split in half — a broken
// rune would make the row's JSON invalid.
func truncateBody(s string) string {
	if len(s) <= subjectBodyBytes {
		return s
	}
	cut := subjectBodyBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
