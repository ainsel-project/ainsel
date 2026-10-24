package eventqueue

import "time"

// Retry pacing for failed tasks.
//
// A nacked task goes back to 'pending' with retry_after set, and the fetch path
// refuses to hand it out until that instant has passed (see Task.RetryAfter).
// The wait grows with the attempts the row has already spent.
//
// Exponential rather than fixed, because the failures this path sees are
// mostly not transient. A task that hits the runtime's per-turn wall clock
// (TURN_TIMEOUT_MS) fails the same way on the next attempt, and so does one
// rejected by a provider quota window — the 429 weekly-limit storm this grew out
// of replayed the same rejection ten times in ten minutes. Every attempt costs
// up to that wall clock of serial agent time plus tokens, so a fixed 60s gap
// spends the whole retry budget re-proving the same failure and holds the
// agent's single slot while doing it. Spacing attempts out gives an operator
// time to notice and a quota window time to move; the cost is that a task which
// would eventually have succeeded takes longer to get there.
const (
	// NakDelayBase is the delay used for a nack whose worker sent no
	// delay_ms, and the value NAK_DELAY_MS defaults to in the pi runner.
	NakDelayBase = 60 * time.Second

	// NakDelayMax caps the growth. Uncapped, the delay reaches hours inside
	// the default 10-attempt budget and a task waiting out a transient outage
	// sits in the queue past the point where it is still useful. 30 minutes is
	// the longest a task should wait before an agent looks at it again.
	NakDelayMax = 30 * time.Minute
)

// NakDelay returns how long a nacked task sits out before its next attempt.
//
// attempts is the count already recorded on the row — 0 for a task's first
// failure. It comes from the row and not from the worker because in
// job-per-task mode every attempt is a fresh Job: the process that nacks has no
// memory of the attempts before it, and only the hub can see the streak.
//
// base is the worker's own preference (nack body delay_ms), where 0 means "the
// runner sent nothing, use the default". The result is base * 2^attempts,
// capped at NakDelayMax: 1m, 2m, 4m, 8m, 16m, 30m, 30m... for the defaults.
func NakDelay(attempts int, base time.Duration) time.Duration {
	if base <= 0 {
		base = NakDelayBase
	}
	delay := base
	for i := 0; i < attempts && delay < NakDelayMax; i++ {
		delay *= 2
	}
	if delay > NakDelayMax {
		return NakDelayMax
	}
	return delay
}
