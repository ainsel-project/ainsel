/**
 * Structural types for the pi objects this extension reads and serializes.
 *
 * These are declared locally instead of imported from pi on purpose, and the
 * reason is the reporting semantics this code exists to protect (issues #75
 * and #76):
 *
 *   - pi's own `Usage` (in `@earendil-works/pi-ai`, not re-exported by
 *     `@earendil-works/pi-coding-agent`) declares `input`, `output`,
 *     `cacheRead`, `cacheWrite` as *required* numbers. This code's whole job is
 *     to survive providers that report them absent, negative or non-numeric, so
 *     typing a parameter as pi's `Usage` would be a lie the runtime guards
 *     contradict.
 *   - Neither `pi-ai` nor `pi-agent-core` is a dependency or peerDependency of
 *     this package, so an import of either would resolve only by accident of
 *     node_modules hoisting.
 *
 * Every field is optional, and "absent" stays distinguishable from "0" — the
 * one product fact the hub reads apart (a reported 0 is a measured free run; no
 * field at all is "never reported").
 *
 * The `_UsageCompat` assertion below is the tripwire: it fails the typecheck if
 * this file drifts from the field names pi's `Usage` declares. Keep
 * `PiUsageFieldsFromPi` in step with pi when the pinned version in
 * `pi/Dockerfile` (`ARG PI_VERSION`) changes — a renamed field must break a
 * build rather than silently under-bill.
 */

/** What pi's `Usage` declares (pi 1.5.2, `@earendil-works/pi-ai`). Reference
 *  shape for the compatibility assertion only; never used as a parameter type. */
interface PiUsageFieldsFromPi {
	input: number;
	output: number;
	cacheRead: number;
	cacheWrite: number;
	totalTokens: number;
	cost: { total: number };
}

/** A pi usage report. All fields optional: providers and pi itself omit them. */
export interface PiUsage {
	input?: number;
	output?: number;
	cacheRead?: number;
	cacheWrite?: number;
	totalTokens?: number;
	cost?: { total?: number };
}

/** A content block of a pi message. Covers the fields the serialiser reads:
 *  text/thinking bodies and the `toolCall` identity fields. Unknown block types
 *  (images, etc.) are reduced to `{ type }`, so nothing else needs typing. */
export interface PiContentBlock {
	type: string;
	text?: string;
	thinking?: string;
	id?: string;
	name?: string;
	arguments?: Record<string, unknown>;
}

/** A pi `AgentMessage`, structurally. pi declares `usage`/`model`/`stopReason`
 *  only on the members that report them, and the runner also builds synthetic
 *  `role: "system"` rows (see `summaryConversationMessage`), so every field is
 *  optional here. */
export interface PiMessage {
	role?: string;
	content?: unknown;
	usage?: PiUsage;
	model?: string;
	stopReason?: string;
	errorMessage?: string;
	toolCallId?: string;
	isError?: boolean;
}

type Assert<T extends true> = T;

/** Compile-time tripwire: every field pi's `Usage` declares must exist on
 *  `PiUsage`. Rename one in pi and this alias stops compiling. */
export type PiUsageCompat = Assert<
	keyof PiUsageFieldsFromPi extends keyof PiUsage ? true : false
>;
