package eventqueue

import "testing"

// The runner's turn timer nacks with exactly this message
// (pi/pi-extensions/ainsel-runner/src/task-loop.ts:
// `Turn timed out after ${turnTimeoutMs}ms`). The literal is asserted on both
// sides of the wire: this test pins the hub's copy of the marker, the runner's
// TS test pins its own template — change one without the other and a suite fails.
func TestIsTurnTimeoutError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{"runner's exact nack literal", "Turn timed out after 21600000ms", true},
		{"short ceiling literal", "Turn timed out after 1800000ms", true},
		{"embedded in a longer message", "task failed: Turn timed out after 600000ms (retrying)", true},
		{"case-insensitive", "turn timed out after 600000ms", true},
		// The pickup-deadline reaper's reason is the platform ending a run that
		// never started — not a turn timeout.
		{"expiry reason is not a turn timeout", "not picked up in time", false},
		{"generic nack is not a turn timeout", "model endpoint down", false},
		{"empty message", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTurnTimeoutError(tc.msg); got != tc.want {
				t.Errorf("IsTurnTimeoutError(%q) = %v, want %v", tc.msg, got, tc.want)
			}
		})
	}
}

// The marker must be the literal phrase from the runner's nack message, not a
// paraphrase: it is what the completed-task badge keys off.
func TestTurnTimeoutMarkerLiteral(t *testing.T) {
	if TurnTimeoutMarker != "Turn timed out after" {
		t.Errorf("TurnTimeoutMarker = %q, want %q", TurnTimeoutMarker, "Turn timed out after")
	}
	if !IsTurnTimeoutError("Turn timed out after 21600000ms") {
		t.Error("the runner's literal must match the marker")
	}
}
