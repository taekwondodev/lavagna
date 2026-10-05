# Browser rounds

[Install lavagna](../README.md#install), then use these commands from the agent's invoking shell. Read [Security](../SECURITY.md) before presenting sensitive material or scripted prototypes.

## Bind a conversation

Identity is `PI_SESSION_ID` plus `PI_SESSION_FILE`, otherwise `LAVAGNA_SESSION`. Another harness opts in by exporting `LAVAGNA_SESSION`.

The page opens through `$BROWSER` when set, otherwise `open`. `$BROWSER` is split on whitespace into a command and its arguments, and the URL is appended. If the command cannot start, lavagna reports it on stderr and keeps waiting on the status-line URL.

## Present and close

- `lavagna check` returns `{"lavagna":"ready"}` and exits 0 when the conversation identity is bindable, otherwise an invalid result with the reason. It does not start a server.
- `lavagna round < round.md` presents a round and blocks until the user sends one feedback batch. Pass no timeout; Esc interrupts.
- `lavagna round DIR` does the same for `DIR/round.md` plus the `.js`, `.css` and image files beside it.
- `lavagna round --help` prints the minimal authoring contract and a decisions-only example. `lavagna round --help grammar` loads the full grammar, components and rich example.
- `lavagna round --reuse rN` reuses that round's content snapshot and accepts new decisions from stdin. Its input must contain only `# Decidere`; previous questions and choices are not inherited.
- `lavagna feedback --help` describes selective access to retained feedback.
- `lavagna close` ends the phase: it shows "Frontiera chiusa, torna al terminale", destroys retained round snapshots, feedback, screenshots and local state, and releases its page origin. Read all needed feedback and images first. Source round directories are not deleted.

Commands that access conversation state also remove the lavagna directories of other conversations untouched for 1 day, skipping any whose call is still live.

## Author a round

`round.md` is lavagna's own grammar, written in Markdown's block syntax; `lavagna round --help grammar` is its full reference. Omit explanatory chapters when unnecessary and batch independent questions into one round. An unanswered question is not approval. Chapters `# Capire`, `# Confrontare` and `# Decidere` hold headings, paragraphs, lists, tables, the components `::: info`, `proposal`, `evidence`, `steps`, `boundary`, `why` and `excerpt`, `{ref="…"}` anchors, and raw HTML or SVG. Decisions are questions with options under `# Decidere`. Inside text only `**bold**` and backtick-delimited code are interpreted. Anything else is `invalid` with line-numbered errors before the page opens.

A round directory holds at most 32 files and 4 MiB including rendered excerpts, under relative paths with no symlinks; `.lavagna` is reserved for the page's own frame assets. The parser refuses raw HTML `src` and `href` values that are not round files, `data:` images or `#` fragments. This early lint is distinct from the [browser security boundary](../SECURITY.md#shell-and-content-boundary).

An excerpt reads `path:start-end` once, from a regular file under the Git root of the working directory, at most 64 KiB. The page shows that snapshot, not a live view of the file.

### Reuse unchanged content

After a round, use its returned `round` ID with `--reuse` and author only new decisions. Lavagna reuses the rendered Capire/Confrontare content, anchors and resource bytes; it does not reread source assets or repository excerpts. This is useful for several decisions about the same comparison or prototype. There is no implicit inheritance and no patch language: when the content changes, submit a new complete round instead. A missing base, a base without content, a directory input or explanatory content supplied together with `--reuse` is invalid. The retained snapshot plus the new rendered decisions must fit a 6 MiB expanded storage budget, including serialization and assets. Round references belong to the current conversation phase and stop being usable after `close` or expiry.

## Collect feedback

The user can choose one option per question, click the selected option again to clear it, attach comments to authored anchors and paste or drop screenshots into the feedback area. Choices are optional; a cleared question is omitted from the submitted choices. Picking an anchor does not activate the prototype's controls, including on a double click. Earlier anchors remain valid in the same conversation so interrupted feedback can be resent.

PNG, JPEG, WebP and GIF screenshots are accepted by file signature, at most 10 MiB each and 8 per batch. Lavagna accepts uploaded bytes, never a URL to fetch or a host path to read. Screenshots remain until `close` or the 1-day sweep, including across interrupted rounds.

Comment text is capped at 32 KiB per batch, measured as encoded JSON text. The page refuses more with a live counter and never truncates. The full feedback record is bounded to 48 KiB. These are admission bounds, not token budgets: large valid feedback is stored privately and accessed selectively rather than automatically filling the agent's context.

## Read the result

`round` writes exactly one JSON outcome to stdout. Its operational status line (round, URL, "Esc per interrompere") goes to stderr; harnesses that merge both streams will still include it in context. `check`, feedback reads and `close` also return machine JSON. Help is explicitly requested text. Esc produces no outcome object.

| Outcome | Stdout | Exit |
| --- | --- | --- |
| feedback | `{"lavagna":"feedback","round":"r3","submission":"s-…","choices":{"storage":"b"},"comments":[{"anchor":"File · contesa","text":"…"},{"anchor":null,"text":"…"}],"images":["/…/lavagna/<key>/images/….png"]}` | 0 |
| deferred feedback | `{"lavagna":"feedback","round":"r3","submission":"s-…","deferred":true,"counts":{"choices":1,"comments":2,"images":0}}` | 0 |
| closed | `{"lavagna":"closed","page":"cleaned"}`, `"page":"unconfirmed"` or `"page":"not-connected"` | 0 |
| invalid | `{"lavagna":"invalid","errors":["round.md:12: unknown block ::: card"]}` | 2 |
| busy | `{"lavagna":"busy","round":"r3"}` | 3 |
| error | `{"lavagna":"error","message":"…"}` | 1 |

`images` holds absolute paths to the batch's screenshots in draft order. Concurrent calls for the same conversation answer `busy` with the live round, or `error` when the lock holder is not a round.

Invalid input reports at most eight bounded, line-numbered diagnostics. A shortened diagnostic ends in `...`; `more` gives the number of further errors. Fix the reported errors before retrying, rather than loading the entire rich grammar for every simple question.

### Read only the feedback you need

Every successful batch is retained losslessly before the round returns. A full result of at most 2 KiB is inline; larger results have `deferred:true` and counts instead of content. Deferred is not empty feedback and counts are not decisions. References are submission IDs scoped to the conversation, not arbitrary filesystem paths.

```sh
lavagna feedback s-0123456789abcdef
lavagna feedback s-0123456789abcdef --comment 2
lavagna feedback s-0123456789abcdef --comment 1 --offset 2048
lavagna feedback s-0123456789abcdef --all
```

The default result is an overview with `items`, `offset`, `next` and `total`. Items are choices (`choice`, `value`), screenshot paths (`image`) or comment descriptors (`comment`, optional `anchor`, `bytes`). Choices are sorted by ID, followed by images in draft order, then comments in authored order. A missing anchor means a general comment. Follow `next` with `--offset` until it is zero; each overview response stays below 4 KiB. No omitted page implies an absent choice or comment.

Comment indices are one-based. `--comment N` returns exact `text`, `offset`, `next` and the full byte `length`. For comment reads, offsets are UTF-8 byte positions, not characters or tokens; copy `next` rather than guessing. Text pages use at most 2 KiB of encoded JSON text and never split a UTF-8 character. `next:0` means complete. An invalid index, offset or foreign/missing reference is an error, not an empty result.

When all feedback is needed, `--all` returns the full record in one response, up to the 48 KiB admission bound. It cannot be combined with selectors. This avoids the extra calls and metadata of reading every comment page. Read all feedback relevant to a decision before acting; selective transport must not become implicit approval or skipped objections. Read any needed screenshots and acquire all necessary text before `close`, which makes these references unavailable. Taking the lease for a feedback read refreshes inactivity.

## Interpret delivery status

The page shows Accepted on the server's reply and Returned once the result line is written. With a Pi session witness it can then show Received, Not read, Turn ended before frontier closure or Turn interrupted. These are delivery observations, not proof that the agent understood the feedback or that the frontier is settled.

A stopped or interrupted agent turn does not close the browser phase. The page keeps the submitted feedback and reconnects to the next round without directing the user back to the terminal. Only `close` shows the completion message. Lavagna does not restart the agent: the invoking workflow must continue after feedback, present explanations and remaining decisions in subsequent rounds, and close only after a clean final confirmation. If the agent stops early, browser continuity alone cannot make it resume.

Without `$PI_SESSION_FILE`, on an unsupported session version or shape, on an unrecognized stop reason, or after 30 minutes without a new session entry, receipts stop at their last witnessed stage and the page says live status is unavailable. A Received page then stops claiming the agent is working. Pi retries and compaction can continue the turn, so `error` and `length` stop reasons do not establish that it ended.

The relay holds the origin during the agent's turn. If the harness owner cannot be identified, no relay starts. If the relay's Unix socket path exceeds the platform limit, lavagna reports it on stderr and receipts stop at Returned. If the owner disappears or cannot be checked, the relay releases the origin and drops the live-status claim.

## Recover an interrupted round

A reload or reopened tab during a gap shows the last round and draft, including rich content. The page reconnects to later calls without a manual refresh. It sends only while its event stream is live; if the stream fails for good, the tab keeps the draft and disables send.

When a stream the tab watched live at Accepted drops before Returned, the page shows Uncertain and restores the batch to the draft for resend. A tab closed or reloaded in that window shows its sent batch while reconnecting and claims no delivery stage until the call or the next round establishes how it ended. A round interrupted without an accepted batch is Interrupted.

When the next round arrives, an unsent draft or an Uncertain batch moves into it once, keeping choices the new round still offers. A returned batch does not. Tabs adopt each other's draft changes rather than letting a stale tab overwrite a sent batch. The draft is frozen while send is in flight.

If another process holds the recorded port, the next call creates a fresh origin and opens a new tab. The old tab says "Questa scheda non è più collegata alla conversazione", disables send and keeps the draft copyable. The new origin starts with empty browser storage; transfer any needed draft manually. This is recovery behavior, not server authentication; see the [accepted port-squatting risk](../SECURITY.md#accepted-residual-risk-local-port-squatting).

`close` is destructive, not a pause. It removes retained local artifacts while keeping the lease file so concurrent calls cannot lock different inodes. A live round remains busy rather than being killed. Repeating `close` is safe. Browser cleanup removes the rendered prototype and feedback UI state as well as draft storage, cached content and the service worker; the command only reports confirmed cleanup after the page acknowledges it. `page:"cleaned"` means a connected page acknowledged storage cleanup; `unconfirmed` means the close event was delivered without a cleanup acknowledgement within the bounded wait; `not-connected` means no page connected. This is not proof about other offline tabs or browser profiles. Local deletion still occurs when the browser is disconnected, but its offline cache cannot be claimed erased.
