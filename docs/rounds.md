# Browser rounds

[Install lavagna](../README.md#install), then use these commands from the agent's invoking shell. Read [Security](../SECURITY.md) before presenting sensitive material or scripted prototypes.

## Bind a conversation

Identity is `PI_SESSION_ID` plus `PI_SESSION_FILE`, otherwise `LAVAGNA_SESSION`. Another harness opts in by exporting `LAVAGNA_SESSION`.

The page opens through `$BROWSER` when set, otherwise `open`. `$BROWSER` is split on whitespace into a command and its arguments, and the URL is appended. If the command cannot start, lavagna reports it on stderr and keeps waiting on the status-line URL.

## Present and close

- `lavagna check` exits 0 when the conversation identity is bindable, otherwise non-zero with the reason.
- `lavagna round < round.md` presents a round and blocks until the user sends one feedback batch. Pass no timeout; Esc interrupts.
- `lavagna round DIR` does the same for `DIR/round.md` plus the `.js`, `.css` and image files beside it.
- `lavagna round --help` prints the round grammar and one example.
- `lavagna close` shows "Frontiera chiusa, torna al terminale", deletes the conversation's lavagna state and screenshots and releases its page origin.

Every `check`, `round` and `close` also removes the lavagna directories of other conversations untouched for 7 days, skipping any whose call is still live.

## Author a round

`round.md` is lavagna's own grammar, written in Markdown's block syntax; `lavagna round --help` is its reference. Chapters `# Capire`, `# Confrontare` and `# Decidere` hold headings, paragraphs, lists, tables, the components `::: info`, `proposal`, `evidence`, `steps`, `boundary`, `why` and `excerpt`, `{ref="…"}` anchors, and raw HTML or SVG. Decisions are questions with options under `# Decidere`. Inside text only `**bold**` and backtick-delimited code are interpreted. Anything else is `invalid` with line-numbered errors before the page opens.

A round directory holds at most 32 files and 4 MiB including rendered excerpts, under relative paths with no symlinks; `.lavagna` is reserved for the page's own frame assets. The parser refuses raw HTML `src` and `href` values that are not round files, `data:` images or `#` fragments. This early lint is distinct from the [browser security boundary](../SECURITY.md#shell-and-content-boundary).

An excerpt reads `path:start-end` once, from a regular file under the Git root of the working directory, at most 64 KiB. The page shows that snapshot, not a live view of the file.

## Collect feedback

The user can choose options, attach comments to authored anchors and paste or drop screenshots into the feedback area. Picking an anchor does not activate the prototype's controls, including on a double click. Earlier anchors remain valid in the same conversation so interrupted feedback can be resent.

PNG, JPEG, WebP and GIF screenshots are accepted by file signature, at most 10 MiB each and 8 per batch. Lavagna accepts uploaded bytes, never a URL to fetch or a host path to read. Screenshots remain until `close` or the 7-day sweep, including across interrupted rounds.

Comment text is capped at 32 KiB per batch, measured as encoded in the result line. The page refuses more with a live counter and never truncates. The server also refuses a batch whose result line would exceed 48 KiB, keeping it inside the 50 KB tail a harness shows the model.

## Read the result

`round` prints one status line (round, URL, "Esc per interrompere"), then its last line: one JSON object. `close` prints only the JSON object. Esc prints nothing.

| Outcome | Last line | Exit |
| --- | --- | --- |
| feedback | `{"lavagna":"feedback","round":"r3","submission":"s-…","choices":{"storage":"b"},"comments":[{"anchor":"File · contesa","text":"…"},{"anchor":null,"text":"…"}],"images":["/…/lavagna/<key>/images/….png"]}` | 0 |
| closed | `{"lavagna":"closed","page":"shown"}` or `"page":"not-connected"` | 0 |
| invalid | `{"lavagna":"invalid","errors":["round.md:12: unknown block ::: card"]}` | 2 |
| busy | `{"lavagna":"busy","round":"r3"}` | 3 |
| error | `{"lavagna":"error","message":"…"}` | 1 |

`images` holds absolute paths to the batch's screenshots in draft order. Concurrent calls for the same conversation answer `busy` with the live round, or `error` when the lock holder is not a round.

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

`close` removes the draft, cached content and service worker as well as the conversation's local state and screenshots.
