// Package signin holds ChatGPT sign-in sessions (issue #89): the hub's relay
// between the console and an agent's short-lived sign-in pod.
//
// A session carries two values and nothing else of substance: the OpenAI
// authorization URL the pod posts, and the redirect URL the user pastes back
// after signing in. The authorization code inside the redirect URL is useless
// without the PKCE verifier, which never leaves the pod, and the redirect URL
// is cleared in the same step that hands it to the pod. Tokens never pass
// through here.
package signin

import (
	"context"
	"errors"
	"time"
)

// State is where a session is in the sign-in flow.
type State string

const (
	// StateWaitingForPod: started; the operator has not yet run the pod, or
	// the pod has not yet posted the authorization URL.
	StateWaitingForPod State = "waiting_for_pod"
	// StateAwaitingRedirect: the authorization URL is ready; waiting for the
	// user to paste the redirect URL.
	StateAwaitingRedirect State = "awaiting_redirect"
	// StateExchanging: the pod has the redirect URL and is exchanging the code.
	StateExchanging State = "exchanging"

	StateSucceeded State = "succeeded"
	StateFailed    State = "failed"
	StateExpired   State = "expired"
	StateCancelled State = "cancelled"
)

// Live reports whether the session can still make progress.
func (s State) Live() bool {
	return s == StateWaitingForPod || s == StateAwaitingRedirect || s == StateExchanging
}

// TTL bounds a session. The sign-in Job's own deadline is longer, so an
// abandoned session expires here first and the pod sees it closed.
const TTL = 10 * time.Minute

// Retention is how long finished sessions are kept for the console to read
// the outcome before Prune removes them.
const Retention = time.Hour

var (
	// ErrNotFound: no session matches.
	ErrNotFound = errors.New("sign-in session not found")
	// ErrLive: the agent already has a live session.
	ErrLive = errors.New("a sign-in is already in progress for this agent")
	// ErrClosed: the session finished, expired or was cancelled.
	ErrClosed = errors.New("sign-in session is closed")
	// ErrWrongState: the session is live but not at the step the call needs.
	ErrWrongState = errors.New("sign-in session is not at this step")
)

// Session is one sign-in attempt. The pasted redirect URL is deliberately not
// a field: it is only ever returned by TakeRedirect, once.
type Session struct {
	ID        string
	Agent     string
	State     State
	AuthURL   string
	Error     string
	CreatedAt time.Time
	ExpiresAt time.Time
	// PodTokenHash is the SHA-256 (hex) of the token only the sign-in pod
	// holds. The internal routes require it: every agent pod has the shared
	// internal token, so that alone must not let one post a link or take the
	// pasted address. Never sent to the console.
	PodTokenHash string
}

// Store persists sessions. Implementations report a live session past its
// expiry as StateExpired, and refuse to advance it.
type Store interface {
	// Start opens a session for agent, or returns ErrLive. podTokenHash is
	// the hash of the sign-in pod's token for this session.
	Start(ctx context.Context, agent, podTokenHash string) (Session, error)
	// Latest returns the agent's most recent session, or ErrNotFound.
	Latest(ctx context.Context, agent string) (Session, error)
	// Get returns one session of the agent, or ErrNotFound.
	Get(ctx context.Context, agent, id string) (Session, error)
	// SetAuthURL records the authorization URL and moves the session from
	// waiting_for_pod to awaiting_redirect.
	SetAuthURL(ctx context.Context, agent, id, url string) error
	// SubmitRedirect stores the pasted redirect URL on the agent's live
	// session, which must be awaiting_redirect.
	SubmitRedirect(ctx context.Context, agent, url string) error
	// TakeRedirect hands the pasted redirect URL to the pod once, clearing it
	// and moving the session to exchanging. It returns "" while nothing has
	// been pasted.
	TakeRedirect(ctx context.Context, agent, id string) (string, error)
	// Finish ends a live session as succeeded or failed. A session already
	// exchanging finishes even past its expiry.
	Finish(ctx context.Context, agent, id string, ok bool, errMsg string) error
	// Cancel ends the agent's live session, if any, unless its code is
	// already being exchanged.
	Cancel(ctx context.Context, agent string) error
	// Prune deletes sessions created before cutoff. It returns the count.
	Prune(ctx context.Context, cutoff time.Time) (int64, error)
}
