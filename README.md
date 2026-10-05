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

- **Ownership.** A conversation's page origin is one loopback port plus a 256-bit capability in the page path, kept in the user cache directory (`lavagna/<key>/state.json`, 0600, directory 0700) so later rounds reuse the same tab. The key is a digest of the identity; pages never see the raw session values. State holds no round content or feedback.
- **Lease.** A non-blocking `flock` makes concurrent calls of one conversation converge on one owner; the others answer `busy` with the live round, or `error` when the holder is not a round. `close` keeps the lock file and deletes everything else, so a racing call can never lock an unlinked file. The kernel releases the lease on SIGKILL, so Esc needs no cleanup and the next call rebinds the recorded port. When another process holds that port, the call mints a fresh origin and opens a new tab.
- **Send gate.** The feedback endpoint takes only POST with the page's Origin, a JSON body and the round token. A pure gate answers accept, duplicate (same receipt, never delivered twice), answered, stale or foreign (409). Host and capability checks guard every request.
- **Delivery stages.** The page shows Accepted on the server's reply and Returned once the result line is written. Stages only move forward, because the event stream and the send reply race. The page sends only while its event stream is live; when the stream fails for good it says the tab is no longer connected and keeps the draft.
- **Drafts.** Drafts live in the tab's `localStorage`, keyed by the round token, so a later conversation that reuses the port never sees them. A changed draft gets a new submission ID; the draft is frozen while a send is in flight. Drafts are deleted when their batch is returned or the conversation closes.
- **Screenshots.** The user pastes or drops screenshots into the feedback area; each uploads on its own to `POST …/images` with the page's Origin, `application/octet-stream` and the round token in `Lavagna-Round` and `Lavagna-Token`. The server accepts PNG, JPEG, WebP and GIF by magic bytes, whatever the file's name or declared type, at most 10 MiB each, and stores them in the conversation's `images/` directory (0600). The capability and round token are checked before the body is read. The send batch names them by ID, at most 8; lavagna never fetches a URL or reads a host path. A paste or drop that breaks a bound adds nothing and shows a refusal. Screenshots stay until `close` or the 7-day sweep, including ones removed from the draft.
- **Sweep.** Taking a conversation's lease touches its directory. A start removes another conversation's directory only when it is still untouched for 7 days once its lock is held, so a live call is never swept: it renames the directory to a `swept-…` tombstone, releases the lock and deletes the tombstone; later sweeps finish any tombstone left behind. A call that meets a sweep of its own conversation waits up to 2 seconds for it and continues on a fresh directory instead of answering `busy`.
- **Fonts.** The font response carries `Access-Control-Allow-Origin` for the opaque-origin content frame planned for rich rounds.

## Development

```sh
go test ./...
```

Page tests drive headless Chrome over `--remote-debugging-pipe` (`internal/cdptest`) with a mock keychain and skip when no Chrome or Chromium is installed. Tests run lavagna with `BROWSER=true`, so no tab opens in your browser.
