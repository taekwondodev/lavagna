// Writes the modelled witness samples through Pi's own SessionManager: the
// entry envelopes come from Pi's serializer, while the message bodies are
// modelled on Pi 1.0.3's documented shapes for turns a live run did not
// produce. answered, next-round and close are cut from a real Pi session with
// cut.mjs instead.
// Regenerate with: node internal/witnesstest/testdata/record.mjs <pi-coding-agent dir>
// e.g. ~/.pi/agent/install/releases/1.0.3/node_modules/@earendil-works/pi-coding-agent
import { mkdtempSync, readdirSync, renameSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const pi = process.argv[2];
if (!pi) throw new Error("usage: node record.mjs <pi-coding-agent dir>");
const { SessionManager } = await import(pathToFileURL(join(pi, "dist/index.js")).href);
const out = dirname(fileURLToPath(import.meta.url));

// The witness tests replace this placeholder with the submission they send.
const submission = "s-0123456789abcdef01234567";
const feedback = `lavagna · round r1 · http://127.0.0.1:50000/s/${"0".repeat(64)}/ · Esc per interrompere\n` +
  `{"lavagna":"feedback","round":"r1","submission":"${submission}","choices":{"storage":"db"},"comments":[{"anchor":null,"text":"ok, ma..."}],"images":[]}\n`;
const usage = { input: 10, output: 5, cacheRead: 0, cacheWrite: 0, totalTokens: 15, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } };
let clock = 1790000000000;

const assistant = (stopReason, content) => ({ role: "assistant", content, api: "anthropic-messages", provider: "anthropic", model: "claude-sonnet-4-5", usage, stopReason, timestamp: clock++ });
const call = (id, command) => assistant("toolUse", [{ type: "toolCall", id, name: "bash", arguments: { command } }]);
const result = (id, text, isError = false) => ({ role: "toolResult", toolCallId: id, toolName: "bash", content: [{ type: "text", text }], isError, timestamp: clock++ });
const said = text => assistant("stop", [{ type: "text", text }]);

const samples = {
  // The turn ends before any tool result carries the batch.
  "unread": [
    result("call_round", "Command aborted", true),
    assistant("aborted", []),
  ],
  // A retryable provider error: Pi keeps the failed attempt and the turn goes on.
  "retry": [
    result("call_round", feedback),
    { ...assistant("error", []), errorMessage: "overloaded_error" },
    call("call_ls", "ls"),
    result("call_ls", "README.md\n"),
    said("Ho registrato la scelta: database locale."),
  ],
  // A stop reason this witness does not know: receipts must degrade.
  "unrecognized": [
    result("call_round", feedback),
    assistant("suspended", [{ type: "text", text: "..." }]),
  ],
};

for (const [name, entries] of Object.entries(samples)) {
  const dir = mkdtempSync(join(tmpdir(), "lavagna-witness-"));
  const session = SessionManager.create("/work/project", dir);
  session.appendMessage({ role: "user", content: "Facciamo il grilling.", timestamp: clock++ });
  session.appendMessage(call("call_round", "lavagna round <<'EOF'\n# Capire\n...\nEOF"));
  session.appendCustomEntry("lavagna-witness-sample", { offset: "the call returns after this entry" });
  for (const message of entries) session.appendMessage(message);
  const [file] = readdirSync(dir).filter(f => f.endsWith(".jsonl"));
  renameSync(join(dir, file), join(out, name + ".jsonl"));
  rmSync(dir, { recursive: true });
}
