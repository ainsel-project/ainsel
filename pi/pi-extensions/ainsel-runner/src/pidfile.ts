/**
 * Announce this runtime to the task pod's sidecars.
 */

import * as fs from "fs";
import * as path from "path";
import { logError } from "./logging.ts";

/**
 * Announce this process to the sidecars of the task pod, so they can end with
 * the task instead of outliving it.
 *
 * The operator gives a per-task Job a shared PID namespace and a shared
 * directory (AINSEL_TASK_DIR). Whatever writes its PID there is the process the
 * pod exists to run, and a sidecar watches that PID and stands down when it
 * disappears. This matters because a Job only completes when every container in
 * its pod exits: a chat sidecar that keeps serving holds the pod Running, the
 * Job never gains a completion condition, and the slot it holds in the agent's
 * concurrency ceiling then blocks both new tasks and this task's own retry.
 *
 * Best effort on purpose. Without AINSEL_TASK_DIR (a long-lived pod, or an
 * operator that predates this contract) there is nothing to announce, and a
 * failure to write must never cost the pod its task — the sidecar then behaves
 * the way it always did.
 */
export function writeAgentPidFile(dir: string | undefined = process.env.AINSEL_TASK_DIR): string | null {
	if (!dir) return null;
	const file = path.join(dir, "agent.pid");
	try {
		// World-readable: the sidecar is a different image and runs as a different
		// UID. chmod rather than relying on the write mode, which umask would eat.
		// Written as a rename so the file never exists before the pid is in it.
		const tmp = `${file}.tmp`;
		fs.writeFileSync(tmp, `${process.pid}\n`);
		fs.chmodSync(tmp, 0o644);
		fs.renameSync(tmp, file);
		return file;
	} catch (err) {
		logError("could not announce pid to sidecars", {
			path: file,
			err: err instanceof Error ? err.message : String(err),
		});
		return null;
	}
}
