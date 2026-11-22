package eventqueue

import "strings"

// TurnTimeoutMarker is the stable phrase the runner puts in the nack message
// when its turn timer kills an attempt:
// pi/pi-extensions/ainsel-runner/src/task-loop.ts builds
// `Turn timed out after ${turnTimeoutMs}ms`. It is the only surviving evidence
// that an attempt died mid-work rather than finishing, so both sides pin it in
// tests — Go (TestIsTurnTimeoutError) and TS (turn-timeout-marker.test.ts) —
// and drift fails loudly instead of quietly un-badging a timed-out run.
const TurnTimeoutMarker = "Turn timed out after"

// IsTurnTimeoutError reports whether a retained task error is the runner's
// turn-timeout nack — the platform's timer ended the attempt, not the agent.
// It matches case-insensitively on the marker; the expiry reason ("not picked
// up in time") and a generic nack ("model endpoint down") are not turn
// timeouts, and neither is the empty string.
func IsTurnTimeoutError(msg string) bool {
	return strings.Contains(strings.ToLower(msg), strings.ToLower(TurnTimeoutMarker))
}
