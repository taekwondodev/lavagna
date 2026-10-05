# lavagna

A harness-neutral CLI for browser rounds in agent conversations: present one round of content, await one explicit feedback batch, and return it to the invoking shell as a compact machine-readable result.

Requirements: [taekwondodev/dev#15](https://github.com/taekwondodev/dev/issues/15). Design route: [taekwondodev/dev#89](https://github.com/taekwondodev/dev/issues/89).

## Install

```sh
make install   # CGO_ENABLED=0 go build -trimpath -o ~/.local/bin/lavagna .
```

The binary uses only the Go standard library and embeds the page, the v3 stylesheet and Atkinson Hyperlegible Next (SIL OFL 1.1, see `internal/page/assets/fonts/`).

## Commands

- `lavagna check` exits 0 when the conversation identity is bindable, otherwise non-zero with the reason.
- `lavagna round < round.md` presents a text-only round in the browser and blocks until the user sends one feedback batch. Pass no timeout; Esc interrupts.
- `lavagna close` shows "Frontiera chiusa, torna al terminale", deletes the conversation's lavagna state and screenshots and releases its page origin.

Every `check`, `round` and `close` also removes the lavagna directories of other conversations untouched for 7 days, skipping any whose call is still live.

Identity is `PI_SESSION_ID` plus `PI_SESSION_FILE`, otherwise `LAVAGNA_SESSION`. Another harness opts in by exporting `LAVAGNA_SESSION`. The page opens through `$BROWSER` when set, otherwise `open`. `$BROWSER` is split on whitespace into a command and its arguments, and the URL is appended; when it cannot start, lavagna says so on stderr and keeps waiting on the status-line URL.

## Output

`round` prints one status line (round, URL, "Esc per interrompere"), then its last line: one JSON object. `close` prints only the JSON object.

| Outcome | Last line | Exit |
|---|---|---|
| feedback | `{"lavagna":"feedback","round":"r3","submission":"s-…","choices":{"storage":"b"},"comments":[{"anchor":null,"text":"…"}],"images":["/…/lavagna/<key>/images/….png"]}` | 0 |
| closed | `{"lavagna":"closed","page":"shown"}` or `"page":"not-connected"` | 0 |
| invalid | `{"lavagna":"invalid","errors":["round.md:12: unknown block ::: card"]}` | 2 |
| busy | `{"lavagna":"busy","round":"r3"}` | 3 |
| error | `{"lavagna":"error","message":"…"}` | 1 |

Esc prints nothing. `images` holds the absolute paths of the batch's screenshots, in the order they appear in the draft. Comment text is capped at 32 KiB per batch, measured as it is encoded in the result line; the page refuses more with a live counter and never truncates. The server also refuses a batch whose result line would exceed 48 KiB, so the line stays inside the 50 KB tail a harness shows the model.

## Round format

`round.md` is lavagna's own grammar, written in Markdown's block syntax. Any other block (tables, code fences or indented code, quotes, HTML blocks, other heading levels, `*`, `+`, `1)` or indented list markers, other `:::` kinds, `{ref="…"}` attributes) is `invalid` with line-numbered errors before the page opens. Inside text only `**bold**` and `` `code` `` are interpreted; everything else, such as `*`, `_`, `[…](…)`, `<`, `&` or `\`, is shown exactly as written.

```text
# Capire | # Confrontare | # Decidere   chapters, in this order, each at most once
## Heading, ### Heading                 section headings
::: info [Label] ... :::                information block, default label "Informazione"
- item, 1. item                         lists; lines indented by two spaces continue an item
**bold**, `code`                        inline
## Question {id="storage"}              under # Decidere: one question
- [a] Option label                      its options (at least two); indented lines are the detail
```

## Design notes

- **Ownership.** A conversation's page origin is one loopback port plus a 256-bit capability in the page path, kept in the user cache directory (`lavagna/<key>/state.json`, 0600, directory 0700) so later rounds reuse the same tab. The key is a digest of the identity; pages never see the raw session values. State holds no round content or feedback, only the origin, the round counter and the live round's lifecycle.
- **Lifecycle.** One pure step function (`internal/conversation/lifecycle.go`) records three events: a round started on a bound origin, a batch accepted, a batch returned. Esc kills the call without cleanup, so the next call reads what it left behind: a live round with an accepted batch ended Uncertain, a live round without one was Interrupted. That outcome travels to the page with the next round.
- **Lease.** A non-blocking `flock` makes concurrent calls of one conversation converge on one owner; the others answer `busy` with the live round, or `error` when the holder is not a round. `close` keeps the lock file and deletes everything else, so a racing call can never lock an unlinked file. The kernel releases the lease on SIGKILL, so Esc needs no cleanup and the next call rebinds the recorded port. When another process holds that port, the call mints a fresh origin and opens a new tab.
- **Send gate.** The feedback endpoint takes only POST with the page's Origin, a JSON body and the round token. A pure gate answers accept, duplicate (same receipt, never delivered twice), answered, stale or foreign (409). Host and capability checks guard every request.
- **Delivery stages.** The page shows Accepted on the server's reply and Returned once the result line is written; the call records Returned only after the write succeeds. Stages only move forward, because the event stream and the send reply race. When a stream the tab watched live at Accepted drops before Returned, the page shows Uncertain and puts the complete batch, including screenshots, back in the draft for resend. A tab reloaded in that window shows its sent batch while reconnecting and claims no delivery stage until the call or next round says how it ended.
- **Continuity.** Nobody holds the origin between calls. A service worker scoped to the conversation's capability path precaches the shell, reconnecting to subsequent calls without a manual refresh. The page reconnects its event stream every 250 ms, including a tab reloaded during a gap. Drafts live in localStorage keyed by capability path; next rounds carry drafts and uncertain batches, but not returned batches. `close` deletes the record, cache and service worker.
- **Screenshots.** The user pastes or drops screenshots into the feedback area; each uploads to `POST …/images` with the page's Origin, `application/octet-stream` and round token. PNG, JPEG, WebP and GIF are accepted by magic bytes, at most 10 MiB each, stored in the conversation's `images/` directory (0600). The send batch names them by ID, at most 8; lavagna never fetches a URL or reads a host path. Persisted screenshot IDs remain valid across server instances for resend. Screenshots stay until `close` or the 7-day sweep.
- **Sweep.** Taking a conversation's lease touches its directory. A start removes another conversation's directory only when it is still untouched for 7 days once its lock is held, so a live call is never swept.
- **Fonts.** The font response carries `Access-Control-Allow-Origin` for the opaque-origin content frame planned for rich rounds.

## Security notes

- **Trust boundary.** The boundary is the user account, including the machine's admin accounts ([decision](https://github.com/taekwondodev/dev/issues/100)). Processes inside it can already read lavagna's 0600 state, the browser profile, `$PI_SESSION_FILE` and the transcript, which holds the status line with the capability URL. The page does not authenticate the server.
- **Accepted residual risk: port squatting.** While nobody holds the origin, another process can bind the conversation's port. The page's next reconnect then presents the capability to it, and a reload loads that process's page. Only processes inside the trusted boundary can do this, so the capability gives them nothing new. The page sends feedback only after a valid `round` event from the stream, so a squatter never receives a batch.
- **Collision handling.** When rebinding the recorded port fails with `EADDRINUSE`, the call mints a fresh origin (port and capability), persists it and opens a new tab. The old tab's stream fails for good: it says "Questa scheda non è più collegata alla conversazione", disables send and keeps the draft copyable, because the new origin starts with empty storage.

## Development

```sh
go test ./...
```

Page tests drive headless Chrome over `--remote-debugging-pipe` (`internal/cdptest`) with a mock keychain and skip when no Chrome or Chromium is installed. Tests run lavagna with `BROWSER=true`, so no tab opens in your browser.
