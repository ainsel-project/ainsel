package tasklogs

import "testing"

func TestValidLevel(t *testing.T) {
	valid := []string{LevelDebug, LevelInfo, LevelWarn, LevelError}
	for _, l := range valid {
		if !ValidLevel(l) {
			t.Errorf("ValidLevel(%q) = false, want true", l)
		}
	}
	// Must agree with task_logs_level_check (migration 0031), which is
	// case-sensitive and rejects the empty string.
	invalid := []string{"", "DEBUG", "Info", "warning", "trace", "debug "}
	for _, l := range invalid {
		if ValidLevel(l) {
			t.Errorf("ValidLevel(%q) = true, want false", l)
		}
	}
}
