package eventqueue

import (
	"testing"
	"time"
)

func TestNakDelay(t *testing.T) {
	// The sequence the default 60s base produces. The 6th and later attempts
	// sit at the cap, which is what keeps a 10-attempt budget from stretching
	// into hours of waiting.
	steps := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 1 * time.Minute},
		{1, 2 * time.Minute},
		{2, 4 * time.Minute},
		{3, 8 * time.Minute},
		{4, 16 * time.Minute},
		{5, 30 * time.Minute},
		{6, 30 * time.Minute},
		{9, 30 * time.Minute},
	}
	for _, s := range steps {
		if got := NakDelay(s.attempts, 0); got != s.want {
			t.Errorf("NakDelay(%d, 0) = %v, want %v", s.attempts, got, s.want)
		}
	}
}

func TestNakDelayHonoursWorkerBase(t *testing.T) {
	// delay_ms is a worker's preference, so it stays the base of the curve
	// rather than being discarded for a hub-side constant.
	if got := NakDelay(0, 5*time.Second); got != 5*time.Second {
		t.Errorf("first attempt: got %v, want 5s", got)
	}
	if got := NakDelay(3, 5*time.Second); got != 40*time.Second {
		t.Errorf("fourth attempt: got %v, want 40s", got)
	}
	// A base above the cap is capped, not honoured: delay_ms is worker input
	// and must not be able to park a task indefinitely.
	if got := NakDelay(0, 2*time.Hour); got != NakDelayMax {
		t.Errorf("oversized base: got %v, want %v", got, NakDelayMax)
	}
}

// TestNakDelayNeverShrinksOrOverflows pins the two ways an exponential helper
// usually breaks: a negative attempt count must not shorten the delay below the
// base, and a large one must not overflow the duration.
func TestNakDelayNeverShrinksOrOverflows(t *testing.T) {
	if got := NakDelay(-5, 0); got != NakDelayBase {
		t.Errorf("negative attempts: got %v, want %v", got, NakDelayBase)
	}
	if got := NakDelay(1<<30, 0); got != NakDelayMax {
		t.Errorf("huge attempts: got %v, want %v", got, NakDelayMax)
	}
	if got := NakDelay(1<<30, time.Hour); got != NakDelayMax {
		t.Errorf("huge attempts with base: got %v, want %v", got, NakDelayMax)
	}
}
