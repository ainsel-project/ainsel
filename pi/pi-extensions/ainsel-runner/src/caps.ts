/**
 * Content size capping for transcript payloads. Depends on `redaction` and
 * calls it *before* truncating: a secret must never straddle the head/tail
 * window boundary of a truncated block. That import direction is load-bearing,
 * not stylistic.
 */

import { redactSecrets } from "./redaction.ts";
import { resolveToolResultMaxChars } from "./config.ts";
import type { PiContentBlock } from "./pi-types.ts";



// ---------------------------------------------------------------------------
// Tool-result content size capping — prevents oversized toolResult content
// (large file reads, command output) from producing huge task_conversations
// rows and transcript payloads. Applied per-text-block with head+tail
// retention so both the beginning and the error tail of output survive.
// ---------------------------------------------------------------------------

/**
 * Truncate a string to `maxChars` using head+tail retention (75% head,
 * 25% tail) with an ASCII marker showing the omitted char count.
 * Returns the input unchanged when it fits within the cap.
 */
export function truncateWithMarker(text: string, maxChars: number): string {
	if (text.length <= maxChars) return text;
	const headLen = Math.floor(maxChars * 0.75);
	const tailLen = maxChars - headLen;
	const omitted = text.length - headLen - tailLen;
	return (
		text.slice(0, headLen) +
		`\n... [truncated ${omitted} chars] ...\n` +
		text.slice(text.length - tailLen)
	);
}

/**
 * Cap toolResult content for transcript serialization.
 *
 * - **string** → redact then truncate as a single text value.
 * - **array** → map blocks: text blocks are redacted then truncated;
 *   non-text blocks (images, etc.) are reduced to `{ type }` (parity
 *   with user/assistant stripping).
 * - **other** (arbitrary objects) → returned unchanged; the caller's
 *   outer `redactSecrets(JSON.stringify(…))` and the per-message
 *   safety-net cap handle those.
 *
 * Redaction runs *before* truncation so a secret can never be split
 * across the head/tail window boundary.
 *
 * @param content  The raw `m.content` from a toolResult message.
 * @param maxChars Per-text-block cap (defaults to `resolveToolResultMaxChars()`).
 */
export function capToolResultContent(content: unknown, maxChars?: number): unknown {
	const cap = maxChars ?? resolveToolResultMaxChars();

	if (typeof content === "string") {
		return truncateWithMarker(redactSecrets(content), cap);
	}

	if (Array.isArray(content)) {
		return (content as unknown[]).map((raw): unknown => {
			const block = raw as PiContentBlock | null | undefined;
			if (block && typeof block === "object" && block.type === "text" && typeof block.text === "string") {
				return { type: "text", text: truncateWithMarker(redactSecrets(block.text), cap) };
			}
			// Non-text blocks (images, etc.) → reduce to { type }.
			if (block && typeof block === "object" && typeof block.type === "string") {
				return { type: block.type };
			}
			return raw;
		});
	}

	// Arbitrary objects: pass through unchanged.
	return content;
}
