import { describe, it } from "node:test";

import assert from "node:assert/strict";

import * as fs from "fs";

import * as os from "os";

import * as path from "path";

import { writeAgentPidFile } from "./pidfile.ts";

describe("writeAgentPidFile", () => {
	it("records this process's pid where the pod's sidecars look", () => {
		const dir = fs.mkdtempSync(path.join(os.tmpdir(), "ainsel-task-"));
		const file = writeAgentPidFile(dir);
		assert.equal(file, path.join(dir, "agent.pid"));
		assert.equal(fs.readFileSync(file as string, "utf8").trim(), String(process.pid));
		// The chat sidecar is a different image running as a different UID; the
		// file has to survive the runner's umask to be readable by it.
		assert.ok(fs.statSync(file as string).mode & 0o004, "pid file is not world-readable");
		// Handed over by rename, so nothing is left behind and the sidecar never
		// sees the file before the pid is in it.
		assert.deepEqual(fs.readdirSync(path.dirname(file as string)), ["agent.pid"]);
	});

	it("is a no-op when the operator set no task dir", () => {
		// A long-lived pod, or an operator from before the per-task contract:
		// nothing to announce, and the sidecar keeps its old behaviour.
		const saved = process.env.AINSEL_TASK_DIR;
		delete process.env.AINSEL_TASK_DIR;
		try {
			assert.equal(writeAgentPidFile(), null);
		} finally {
			if (saved === undefined) delete process.env.AINSEL_TASK_DIR;
			else process.env.AINSEL_TASK_DIR = saved;
		}
	});

	it("returns null rather than throwing when the directory is unusable", () => {
		// Announcing itself is never worth losing the task over.
		const missing = path.join(os.tmpdir(), `ainsel-absent-${process.pid}-${Date.now()}`);
		assert.equal(writeAgentPidFile(missing), null);
	});
});
