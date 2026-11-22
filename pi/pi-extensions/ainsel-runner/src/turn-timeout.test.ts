import { describe, it } from "node:test";

import assert from "node:assert/strict";

import { TURN_TIMEOUT_MARKER, turnTimeoutMessage } from "./config.ts";

// The hub names a completed task's provenance from this exact phrase
// (services/hub/internal/eventqueue/timeout.go, TurnTimeoutMarker) and pins the
// same literal in Go. Keep the two in lockstep: change one without the other and
// a timed-out run stops being badged, which is the bug #392 is about.
describe("turn timeout nack message", () => {
	it("is the literal the hub's predicate matches", () => {
		assert.equal(TURN_TIMEOUT_MARKER, "Turn timed out after");
		assert.equal(turnTimeoutMessage(21_600_000), "Turn timed out after 21600000ms");
		assert.equal(turnTimeoutMessage(600_000), "Turn timed out after 600000ms");
	});

	it("starts with the marker whatever the ceiling", () => {
		assert.ok(turnTimeoutMessage(1).startsWith(TURN_TIMEOUT_MARKER));
	});
});
