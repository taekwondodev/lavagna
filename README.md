# lavagna

A harness-neutral CLI for browser rounds in agent conversations: present one round of content, await one explicit feedback batch, and return it to the invoking shell as a compact machine-readable result.

Requirements: [taekwondodev/dev#15](https://github.com/taekwondodev/dev/issues/15). Design route: [taekwondodev/dev#89](https://github.com/taekwondodev/dev/issues/89). Trust boundary and accepted risks: [Security](SECURITY.md).

## Install

```sh
make install   # CGO_ENABLED=0 go build -trimpath -o ~/.local/bin/lavagna .
```

The binary uses only the Go standard library and embeds the page, the v3 stylesheet and Atkinson Hyperlegible Next (SIL OFL 1.1, see `internal/page/assets/fonts/`).

## Commands

- `lavagna check` exits 0 when the conversation identity is bindable, otherwise non-zero with the reason.
- `lavagna round < round.md` presents a round in the browser and blocks until the user sends one feedback batch. Pass no timeout; Esc interrupts.
- `lavagna round DIR` does the same for `DIR/round.md` plus the `.js`, `.css` and image files beside it.
- `lavagna round --help` prints the round grammar and one example.
- `lavagna close` shows "Frontiera chiusa, torna al terminale", deletes the conversation's lavagna state and releases its page origin.

Identity is `PI_SESSION_ID` plus `PI_SESSION_FILE`, otherwise `LAVAGNA_SESSION`. Another harness opts in by exporting `LAVAGNA_SESSION`. The page opens through `$BROWSER` when set, otherwise `open`. `$BROWSER` is split on whitespace into a command and its arguments, and the URL is appended; when it cannot start, lavagna says so on stderr and keeps waiting on the status-line URL.

## Output

`round` prints one status line (round, URL, "Esc per interrompere"), then its last line: one JSON object. `close` prints only the JSON object.

| Outcome | Last line | Exit |
|---|---|---|
| feedback | `{"lavagna":"feedback","round":"r3","submission":"s-…","choices":{"storage":"b"},"comments":[{"anchor":"File · contesa","text":"…"},{"anchor":null,"text":"…"}],"images":[]}` | 0 |
| closed | `{"lavagna":"closed","page":"shown"}` or `"page":"not-connected"` | 0 |
| invalid | `{"lavagna":"invalid","errors":["round.md:12: unknown block ::: card"]}` | 2 |
| busy | `{"lavagna":"busy","round":"r3"}` | 3 |
| error | `{"lavagna":"error","message":"…"}` | 1 |

Esc prints nothing. Comment text is capped at 32 KiB per batch, measured as it is encoded in the result line; the page refuses more with a live counter and never truncates. The server also refuses a batch whose result line would exceed 48 KiB, so the line stays inside the 50 KB tail a harness shows the model.

## Round format

`round.md` is lavagna's own grammar, written in Markdown's block syntax; `lavagna round --help` is its reference. Chapters `# Capire`, `# Confrontare` and `# Decidere` hold headings, paragraphs, lists, tables, the components `::: info`, `proposal`, `evidence`, `steps`, `boundary`, `why` and `excerpt`, `{ref="…"}` anchors, and raw HTML or SVG; decisions are questions with options under `# Decidere`. Inside text only `**bold**` and `` `code` `` are interpreted. Anything else is `invalid` with line-numbered errors before the page opens.

A round directory holds at most 32 files and 4 MiB including rendered excerpts, under relative paths with no symlinks; `.lavagna` is reserved for the page's own frame assets. The parser refuses raw HTML `src` and `href` values that are not round files, `data:` images or `#` fragments, so a mistake fails before the page opens; the frame's CSP is what enforces it. An excerpt reads `path:start-end` once, from a regular file under the git root of the working directory, at most 64 KiB, and the page shows that snapshot.

## Design notes

- **Ownership.** A conversation's page origin is one loopback port plus a 256-bit capability in the page path, kept in the user cache directory (`lavagna/<key>/state.json`, 0600, directory 0700) so later rounds reuse the same tab. The key is a digest of the identity; pages never see the raw session values. State holds no round content or feedback, only the origin, the round counter, the live round's lifecycle and authored anchor names retained for draft recovery.
- **Lifecycle.** One pure step function (`internal/conversation/lifecycle.go`) records three events: a round started on a bound origin, a batch accepted, a batch returned. Esc kills the call without cleanup, so the next call reads what it left behind: a live round with an accepted batch ended Uncertain, a live round without one was Interrupted. That outcome travels to the page with the next round.
- **Lease.** A non-blocking `flock` makes concurrent calls of one conversation converge on one owner; the others answer `busy` with the live round, or `error` when the holder is not a round. `close` keeps the lock file and deletes everything else, so a racing call can never lock an unlinked file. The kernel releases the lease on SIGKILL, so Esc needs no cleanup and the next call rebinds the recorded port. When another process holds that port, the call mints a fresh origin and opens a new tab.
- **Send gate.** The feedback endpoint takes only POST with the page's Origin, a JSON body and the round token. A pure gate answers accept, duplicate (same receipt, never delivered twice), answered, stale or foreign (409). Host and capability checks guard every request.
- **Content frame.** Capire and Confrontare render in `<iframe sandbox="allow-scripts">` with an opaque origin; Decidere, the editor and send stay in the page. The frame is served under its own per-round key, never the capability or the round token, and every frame response repeats the sandbox in its CSP: `sandbox allow-scripts; default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; font-src 'self'; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors 'self'`. The page's own frame policy allows the local origin and in-memory blob snapshots, but prevents navigation to another network origin. The maintainer accepts WebRTC/UDP egress from round scripts, with no separate script opt-in or mitigation. Scripts can read the frame's content, including code excerpts, so it must not be assumed free of sensitive data. See [the accepted risk and its limits](SECURITY.md#accepted-residual-risk-webrtc). Content can render, run its round scripts and propose anchors; it cannot read the page, send or forge feedback, make external HTTP or WebSocket requests, submit forms, navigate the page or itself to another origin, open popups or use storage. `internal/live/isolation_test.go` reproduces each attempt of the [isolation decision](https://github.com/taekwondodev/dev/issues/95).
- **Anchors.** A helper script in the frame proposes anchors and reports the frame's height through `postMessage`. The page accepts a message only from the frame's window, and an anchor only while the user is picking and only when the round defines it. The server accepts only names authored in this conversation, including earlier rounds so an interrupted anchored draft can be resent without losing its reference. While picking, and for half a second after picking ends, the helper intercepts pointer, keyboard and form events before round scripts see them, so choosing a prototype control, even with a double click, does not activate it.
- **Fonts.** The font response carries `Access-Control-Allow-Origin`, because the content frame's origin is opaque.
- **Delivery stages.** The page shows Accepted on the server's reply and Returned once the result line is written; the call records Returned only after the write succeeds. Stages only move forward, because the event stream and the send reply race. When a stream the tab watched live at Accepted drops before Returned, the call died in between: the page shows Uncertain and puts the batch back in the draft for resend. A tab that was closed or reloaded in that window shows its sent batch while reconnecting and claims no delivery stage until the call or the next round says how it ended. The page sends only while its event stream is live; when the stream fails for good it says the tab is no longer connected and keeps the draft.
- **Continuity.** Nobody holds the origin between calls. A service worker scoped to the conversation's capability path precaches the shell; it answers from the network first and from the cache only when nothing listens, and never touches the event stream or sends. The shell also caches the rich document and its named resources. An opaque sandboxed frame cannot rely on service-worker subresource delivery, so offline restoration builds an in-memory document with embedded resources and the same sandbox. Its original scripts run from exact-byte, integrity-checked copies; its policy still blocks external HTTP and WebSocket connections. The saved resources are deleted with the conversation cache at `close`. The page reconnects its event stream every 250 ms, including a tab reloaded during a gap, so it attaches to the next call on the recorded port before that call would open another tab.
- **Drafts.** The tab's `localStorage` keeps one record per capability path, so a later conversation that reuses the port never sees it: the round on screen and its draft. A reload or a reopened tab while nobody holds the origin shows that round and draft again. Tabs write the record only when their draft changes and adopt each other's writes, so a stale tab never overwrites a sent batch. A changed draft gets a new submission ID; the draft is frozen while a send is in flight. When the next round arrives, a draft that was never sent, or an Uncertain batch, moves into it once, keeping the choices the new round still offers; a returned batch does not. `close` deletes the record, the cache and the service worker.

## Security notes

- **Trust boundary.** The boundary is the user account, including the machine's admin accounts ([decision](https://github.com/taekwondodev/dev/issues/100)). Processes inside it can already read lavagna's 0600 state, the browser profile, `$PI_SESSION_FILE` and the transcript, which holds the status line with the capability URL. The page does not authenticate the server.
- **Accepted residual risk: port squatting.** While nobody holds the origin, another process can bind the conversation's port. The page's next reconnect then presents the capability to it, and a reload loads that process's page. Only processes inside the trusted boundary can do this, so the capability gives them nothing new. The page sends feedback only after a valid `round` event from the stream, so a squatter never receives a batch.
- **Collision handling.** When rebinding the recorded port fails with `EADDRINUSE`, the call mints a fresh origin (port and capability), persists it and opens a new tab. The old tab's stream fails for good: it says "Questa scheda non è più collegata alla conversazione", disables send and keeps the draft copyable, because the new origin starts with empty storage.

## Development

```sh
go test ./...
```

Page tests drive headless Chrome over `--remote-debugging-pipe` (`internal/cdptest`) with a mock keychain and skip when no Chrome or Chromium is installed. Tests run lavagna with `BROWSER=true`, so no tab opens in your browser.
