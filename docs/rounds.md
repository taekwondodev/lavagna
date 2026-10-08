# Browser rounds

Use lavagna from the agent's invoking shell. [README](../README.md#install) covers installation; [Authoring questions](authoring.md) covers content. Read [Security](../SECURITY.md) before presenting sensitive material or scripted prototypes.

## Bind a conversation

Identity is `PI_SESSION_ID` plus `PI_SESSION_FILE`, otherwise `LAVAGNA_SESSION`. Another harness opts in by exporting `LAVAGNA_SESSION`. The page opens through `$BROWSER` when set, otherwise `open`; the URL is appended to the command.

## Present and close

```sh
lavagna check
lavagna round < round.md
# Read feedback and continue the phase as needed.
lavagna close
```

- `check` returns `{"lavagna":"ready"}` when the conversation identity is bindable, otherwise an invalid result.
- `round` applies one call to the phase and waits for a feedback batch. Pass no timeout; Esc interrupts. `round DIR` reads `DIR/round.md` and its question-scoped resources.
- `round --help` prints the minimal format; `round --help grammar` prints the syntax reference. `feedback --help` describes retained feedback reads.
- `close` ends the phase and deletes its retained data. Read needed feedback and images first. Source directories are not deleted. See [recovery](#recover-an-interrupted-round) for browser cleanup and restarting.

Commands accessing conversation state sweep other conversations untouched for one day, skipping any with a live lease. Concurrent calls in one conversation report busy.

## Continue a phase

The [question ledger](../CONTEXT.md) retains questions across calls. After the first call, send only what changes. Call elements stand outside questions, start at the beginning of a line and close with `:::`.

```text
::: settled storage
No new dependency and no wait between sessions.
:::
::: reply crash
Rename is atomic within one file system.
:::
# Who runs the cleanup? {id="cleanup" after="retention"}
## Decidere
- [sweep] The sweep at startup {recommended}
- [manual] A manual command
```

This example continues a phase that already contains `storage`, `crash` and `retention`.

| Element | Effect |
| --- | --- |
| `::: phase TITLE` | Names the phase; valid only in the first call. |
| A new question with a body | Opens the next round. Every unsettled question of the current round moves into it, answered or not; the old round marks it moved. |
| A whole question with a known id | Replaces it in place. The thread stays; the recorded choice survives when its option id does, and a free-text answer always survives. A replaced settled question reopens in the current round, its old round marks it reopened and its decisions row is struck through. |
| A bare title | Plans a question, or updates a planned one; a question with a body is replaced only by a whole question. |
| `::: reply ID` or `::: reply` | Posts the body as an agent message in that question's discussion, closed rounds included, or in the Overview. |
| `::: settled ID [OPTION]` | Closes the question with the recorded answer, or with that option of the current version. The body is the Why of its decisions row, which also copies the title, decision, round and rejected options. Settling again replaces the decision. |
| `::: recap` | Inside a question, renders the decisions table after this call's settles. Write risks, evidence and confirm-or-correct options around it. |
| No element | Resumes waiting, for example after Esc. |

Elements apply in the order phase, settled, replacements, reply, new questions, recap. A reply cannot address a question new in the same call. Replies, settles and replacements stay in the current round. Any invalid element rejects the whole call without changing the ledger or stored questions. One agent message is bounded to 32 KiB; [Authoring](authoring.md#input-limits-and-errors) lists content bounds.

## Answer on the page

The page has a rail of rounds, an active question and its discussion. The title bar shows the phase title and delivery stage; the footer holds the draft summary and Send to Agent. <kbd>⌘</kbd><kbd>↩</kbd> also sends.

### Navigate and decide

The rail lists the Overview, closed rounds, the current round marked CURRENT, and planned questions in a locked round marked "dopo Qn". A card shows unseen content, new agent replies, message count and choice. Closed rounds keep struck cards for moved or reopened questions.

The first question opens initially and when a call advances the round. Replies, replacements and reloads retain the selected question. Clicking a card switches the center; replacing a question redraws that question and marks it unseen.

03 shows the options and recommendation. Clicking the selected option clears it; typing a free-text answer replaces the option. Settled questions and questions in closed rounds can still be read and discussed, but their decision is fixed. The Overview and confirmation recap show the decisions table, with reopened decisions struck through and horizontal scrolling in narrow columns.

### Discuss and send

Each question and the Overview have a discussion. Sent user messages read "Tu"; agent replies come from calls. Added messages and pasted or dropped screenshots stay in the phase draft until Send submits them together with current answers. An agent reply changes only its discussion.

The draft survives calls, so text can be written while the agent works. It freezes while Send awaits lavagna's reply; Send then stays disabled until the next call. Screenshots can be attached only until Send is accepted. A counter warns as draft text approaches the [feedback bound](#collect-feedback).

### Fit the page

Espandi on a fitted block hides the rail and discussion to widen the content. At phone width it shows the block full screen at native size, scrollable in both directions under the shell's Riduci bar. Riduci, <kbd>Esc</kbd> or switching questions ends expansion.

Around 760 px, the rail shrinks to question numbers and discussion remains on the right. At phone width the rail becomes cards below the title bar and discussion opens as a sheet from the footer.

## Collect feedback

One Send submits choices or free-text answers for the current round's open questions, messages and screenshots on any question, and optional Overview feedback. Closed-round questions carry only messages and screenshots. An unanswered question is not approval.

| Feedback | Limit |
| --- | --- |
| Text, including free-text answers | 32 KiB per batch |
| Complete retained record | 48 KiB |
| Screenshots | 8 per batch, up to 10 MiB each |

Screenshots must be PNG, JPEG, WebP or GIF, checked by file signature. Lavagna accepts uploaded bytes, not a URL or host path to fetch. These are admission bounds, not token budgets. Screenshots remain until close or the one-day sweep.

## Read the result

`round` writes exactly one JSON outcome to stdout; operational status goes to stderr. Small feedback results are grouped by question:

```json
{
  "lavagna": "feedback",
  "round": "r3",
  "submission": "s-3f9543fe0510ab8e",
  "questions": {
    "crash": {
      "choice": "journal",
      "messages": ["Does rename stay atomic on NFS?"],
      "images": ["/Users/x/Library/Caches/lavagna/k3f9/images/9c1e.png"]
    },
    "retention": {"answer": "Two days."}
  },
  "overview": {"messages": ["The diagram is clear."]}
}
```

A question has `choice` or `answer`, never both; it may also have `messages` and `images`. Empty fields are omitted. Overview contains messages and images. Image paths are absolute and retain draft order.

Above 2 KiB, the result is deferred: choices remain visible, messages and images become counts, and a free-text answer becomes `true`. The full record is retained before success is returned.

```sh
lavagna feedback s-3f9543fe0510ab8e                  # deferred summary
lavagna feedback s-3f9543fe0510ab8e --question crash # one question, complete
lavagna feedback s-3f9543fe0510ab8e --overview       # Overview feedback
lavagna feedback s-3f9543fe0510ab8e --all            # full record
```

Selectors are mutually exclusive. An unknown id or a question without feedback in that batch is an error. Read all feedback relevant to a decision and needed images before acting; `close` removes these local references. A deferred receipt does not establish that the agent read every message.

## Interpret delivery status

Delivery observations belong to a call, not a round. A reply-only call has its own delivery lifecycle within the same round. Server and CLI events establish acceptance and return; a supported Pi session witness can establish receipt and turn completion. None proves understanding or phase completion. Without a witness the footer adds "stato in tempo reale non disponibile".

The title bar shows a short stage and the footer a sentence, followed by "· N in bozza" when items are staged.

| Observation | Title bar | Footer |
| --- | --- | --- |
| Nothing sent in this call | Tocca a te | Draft summary, or "Niente in bozza" |
| Accepted by lavagna | Inviato | Ricevuto da lavagna · non ancora consegnato all’agente |
| Returned to the terminal | Consegnato al terminale | Consegnato al terminale |
| Harness receipt observed | L’agente lavora | Letto dall’agente · l’agente lavora |
| Turn ended or interrupted | Grilling ancora aperto, Turno interrotto | Whether receipt was observed first |
| Uncertain | Consegna non riuscita | Sent messages returned to the draft |

## Recover an interrupted round

The relay holds the page origin during the agent's turn. After an interruption, reload and inspect delivery status before retrying. A call with no element resumes waiting without resending content. A returned batch is not resent automatically.

### Reload or reconnect

A reload restores the rail, selected question, threads, drafts and seen marks. Every question is cached on arrival for offline rereading. While the event stream is down, Send and its shortcut stay disabled; drafts remain intact and the page reconnects to later calls automatically.

A tab reloaded after Send but before return shows the submitted messages as "Tu" and "Riconnessione a lavagna…", without claiming a delivery stage. If a live stream drops after Accepted but before Returned, the batch is Uncertain: messages and screenshots return to the draft before text written after Send. A newer choice stays. Once the ledger contains that submission, its messages appear as sent and leave the draft; resending does not duplicate them.

### Reconcile changes and tabs

- Moving a question into a new round preserves its draft.
- Rewriting a question preserves a choice only if its option survives. Otherwise the discussion explains that the choice no longer exists in the new version.
- Settling a question drops a different staged answer and explains that it was not sent, inviting a message to request reopening.
- Tabs share a phase draft. A tab from an earlier call cannot overwrite a later call's draft.
- If another process holds the port, the next call opens a new origin. The old tab becomes read-only with Copia bozza for staged text grouped by question and Overview. The new origin starts with an empty draft and rebuilds rail and threads from the call.
- A tab from another build reloads once. An unreadable draft is offered once through Copia bozza, then discarded.

### Close or restart

`close` is destructive and repeatable. It removes local state, question artifacts, feedback and screenshots even without a connected browser. A connected page also deletes its draft, seen marks, cached content and service worker. Browser cleanup is confirmed only after that page acknowledges it; an offline copy may remain. [Security](../SECURITY.md#retained-data) explains the limits of deletion.

After close, expiry or an older-format restart, send complete questions again. A reply or settle referencing a missing question is invalid.

State from an older lavagna restarts the phase on the same origin, deletes the old phase data and reports the restart on stderr. State from a newer lavagna makes `round` and `feedback` return an error asking for `lavagna close`, without modifying it; `close` works on every format. A retained record can be read as its version wrote it with `feedback S --all`; selectors on a record older than per-question feedback fail with a suggestion to use `--all`.
