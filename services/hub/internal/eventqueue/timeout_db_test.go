package eventqueue

import (
	"context"
	"testing"
	"time"
)

// AckTask deliberately does not clear agent_tasks.error. That column is the
// only durable evidence that an earlier attempt died mid-work, and the display
// projection turns it into TaskState.LastError + timedOut — so clearing it on
// ack would delete the very signal #392 exists to surface. This test pins the
// retention: a future "clear on ack" fails here instead of quietly re-creating
// the bug where a 6 h turn timeout followed by a 9-min retry reads as a clean
// success.
//
// Skipped unless TEST_DB_URL is set (see the package header for the throwaway
// Postgres to point it at).
func TestAckTaskRetainsTurnTimeoutError(t *testing.T) {
	ctx := context.Background()
	pool := migratePool(t)
	s := NewStore(pool)

	const nackMsg = "Turn timed out after 21600000ms"
	suffix := time.Now().UTC().Format("150405.000000000")
	eventID := "test-reap-" + suffix
	agent := "test-reap-" + suffix
	insertTestEvent(t, pool, eventID)

	if err := s.EnqueueTask(ctx, Task{
		EventID:     eventID,
		AgentName:   agent,
		TriggerName: "test-trigger",
		Headers:     []byte(`{}`),
		Payload:     []byte(`{}`),
	}); err != nil {
		t.Fatalf("EnqueueTask: %v", err)
	}

	var taskID int64
	if err := pool.QueryRow(ctx,
		`SELECT id FROM agent_tasks WHERE event_id = $1 AND agent_name = $2`, eventID, agent).Scan(&taskID); err != nil {
		t.Fatalf("find task: %v", err)
	}

	if err := s.NakTask(ctx, taskID, time.Second, nackMsg); err != nil {
		t.Fatalf("NakTask: %v", err)
	}
	if err := s.AckTask(ctx, taskID); err != nil {
		t.Fatalf("AckTask: %v", err)
	}

	var status, retained string
	var attempts int
	if err := pool.QueryRow(ctx,
		`SELECT status, attempts, COALESCE(error, '') FROM agent_tasks WHERE id = $1`, taskID).
		Scan(&status, &attempts, &retained); err != nil {
		t.Fatalf("query task: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want completed", status)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (the nack counts the failed attempt)", attempts)
	}
	if retained != nackMsg {
		t.Errorf("retained error = %q, want %q (AckTask must not clear it)", retained, nackMsg)
	}
	if !IsTurnTimeoutError(retained) {
		t.Errorf("the retained nack must read as a turn timeout, got %q", retained)
	}

	// The runner's fetch path still hands the worker the same field name and
	// value: the retry uses it to recognise what killed the previous attempt.
	got, err := s.GetTask(ctx, taskID, agent)
	if err != nil || got == nil {
		t.Fatalf("GetTask: %v (%v)", err, got)
	}
	if got.Status != "completed" || got.Error != nackMsg {
		t.Errorf("GetTask = (%q, %q), want (completed, %q)", got.Status, got.Error, nackMsg)
	}
}
