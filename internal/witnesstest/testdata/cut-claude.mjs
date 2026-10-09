// Cuts a witness sample out of a real Claude Code transcript. It keeps the
// entry that ran the round call and every entry after it verbatim, except
// that attachments keep only their type, since they carry the system prompt
// and account context, and the submission ID becomes the placeholder the
// tests replace. With "interrupt", it appends a user interruption modelled on
// the entries Claude Code 2.1.285 to 2.1.291 wrote when the user pressed Esc.
// Usage: node internal/witnesstest/testdata/cut-claude.mjs <transcript.jsonl> <name> <round entry> <last entry> [interrupt]
import { readFileSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const [path, name, first, last, interrupt] = process.argv.slice(2);
if (!last) throw new Error("usage: node cut-claude.mjs <transcript.jsonl> <name> <round entry> <last entry> [interrupt]");
const placeholder = "s-0123456789abcdef01234567";
const lines = readFileSync(path, "utf8").trimEnd().split("\n");
const kept = lines.slice(Number(first) + 1, Number(last) + 1).map(line => {
  const entry = JSON.parse(line);
  if (entry.type !== "attachment") return line;
  entry.attachment = { type: entry.attachment.type };
  return JSON.stringify(entry);
});
const submission = kept.join("\n").match(/\\"submission\\":\\"(s-[0-9a-f]+)\\"/);
if (!submission) throw new Error("no submission in the entries after the round call");
if (interrupt) {
  const previous = JSON.parse(kept[kept.length - 1]);
  kept.push(JSON.stringify({
    parentUuid: previous.uuid, isSidechain: false, type: "user",
    message: { role: "user", content: [{ type: "text", text: "[Request interrupted by user]" }] },
    uuid: "00000000-0000-4000-8000-000000000001", timestamp: previous.timestamp,
    userType: "external", entrypoint: "cli", sessionId: previous.sessionId, version: previous.version,
  }));
}
const marker = JSON.stringify({ type: "custom", customType: "lavagna-witness-sample", data: { offset: "the call returns after this entry" } });
const sample = [lines[Number(first)], marker, ...kept].join("\n").replaceAll(submission[1], placeholder) + "\n";
writeFileSync(join(dirname(fileURLToPath(import.meta.url)), name + ".jsonl"), sample);
