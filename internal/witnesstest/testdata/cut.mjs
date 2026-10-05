// Cuts a witness sample out of a real Pi session file, keeping every entry Pi
// wrote after the round call verbatim except the submission ID, which becomes
// the placeholder the tests replace.
// Usage: node internal/witnesstest/testdata/cut.mjs <session.jsonl> <name> <round entry> <last entry>
// <round entry> is the index of the assistant entry that ran `lavagna round`.
import { readFileSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const [path, name, first, last] = process.argv.slice(2);
if (!last) throw new Error("usage: node cut.mjs <session.jsonl> <name> <round entry> <last entry>");
const placeholder = "s-0123456789abcdef01234567";
const lines = readFileSync(path, "utf8").trimEnd().split("\n");
const kept = lines.slice(Number(first) + 1, Number(last) + 1);
const submission = kept.join("\n").match(/\\"submission\\":\\"(s-[0-9a-f]+)\\"/);
if (!submission) throw new Error("no submission in the entries after the round call");
const marker = JSON.stringify({ type: "custom", customType: "lavagna-witness-sample", data: { offset: "the call returns after this entry" } });
const sample = [lines[0], lines[Number(first)], marker, ...kept].join("\n").replaceAll(submission[1], placeholder) + "\n";
writeFileSync(join(dirname(fileURLToPath(import.meta.url)), name + ".jsonl"), sample);
