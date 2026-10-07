# Browser rounds

[Install lavagna](../README.md#install), then use these commands from the agent's invoking shell. Read [Security](../SECURITY.md) before presenting sensitive material or scripted prototypes.

## Bind a conversation

Identity is `PI_SESSION_ID` plus `PI_SESSION_FILE`, otherwise `LAVAGNA_SESSION`. Another harness opts in by exporting `LAVAGNA_SESSION`. The page opens through `$BROWSER` when set, otherwise `open`; the URL is appended to the command.

## Present and close

- `lavagna check` returns `{"lavagna":"ready"}` when the conversation identity is bindable; otherwise it returns an invalid result.
- `lavagna round < round.md` applies one call to the phase and waits for the next feedback batch. Pass no timeout; Esc interrupts.
- `lavagna round DIR` reads `DIR/round.md` and its question-scoped resources.
- `lavagna round --help` prints the minimal format; `lavagna round --help grammar` prints the authoring reference.
- `lavagna feedback --help` describes retained feedback reads.
- `lavagna close` ends the phase and removes retained question artifacts, feedback, screenshots and local state. Read needed feedback and images first. Source directories are not deleted.

Commands that access conversation state sweep other conversations untouched for one day, skipping any with a live lease.

## Author questions

A level-1 title starts a question and requires an id. Several questions can be sent in one call. A title with no body plans a question without creating an artifact; a later call can provide its complete contents with the same id.

```markdown
# What should happen if a session crashes? {id="crash" after="storage"}
A short lead explaining the problem.

## Capire
The save truncates the file before writing a replacement.

::: excerpt internal/store/store.go:40-46 45!1
`os.WriteFile` truncates before writing.
:::

## Confrontare
Compare the alternatives and their tradeoffs.

## Decidere
- [atomic] Write a temporary file, then rename {recommended}
  The file is always old or new; one fsync per save.
  => The old file stays intact until rename.
- [journal] Append records and recover the last valid record

A few lines, and the format does not change.

# What should be retained? {id="retention"}
```

Ids match `[a-z0-9][a-z0-9_-]{0,39}` and are unique within the phase. Titles are one line and at most 120 characters. `after` names known questions and dependencies cannot cycle. Chapters `## Capire`, `## Confrontare`, and `## Decidere` are optional except that a complete question needs `## Decidere`; keep them in this order. A complete question has 2–6 options (`- [id] label`), with at most one `{recommended}` marker and at most one indented `=> consequence` per option; other indented lines describe the option, and a paragraph after the options gives the reason for the recommendation. A free-text answer is always available without syntax. The option id `now` is reserved for diagram states.

The page shows the title, the lead (text before the first chapter) and Decidere as 03 in its own controls, as plain text with `` `code` `` and `**bold**`. Capire and Confrontare render as 01 and 02 in the question's content frame; an empty chapter is hidden, and a question with neither has no frame. 02 opens with one Anteprima chip per option and the `=>` effect line of the variant shown: the chip previewed, else the choice, else the recommended option. A free-text answer shows the recommended variant with a note that the agent draws it next turn.

Question scripts follow the choice without asking for it. The frame's `<html>` carries `data-option` (the selected option id, absent when none), `data-free` (`true` when a free-text answer exists) and `data-variant` (the option 02 shows), and `document` receives a `lavagna:option` event with `detail: {option, free, variant}` on load and on every change, so CSS alone can follow the choice. The frame never receives the free text, messages or screenshots; see [Security](../SECURITY.md#accepted-residual-risk-unsent-choice-visible-to-round-scripts).

A raw HTML block, consecutive lines starting with a tag, renders inside `<div class="raw"><div class="raw-stage">`. Give a screen its native width: when it is wider than the column, the page fits it at every width and shows its width and scale above it, with "Espandi a tutta pagina" when the scale falls below 85 %.

A `45!1` excerpt mark identifies line 45 as a problem with badge 1; multiple marks are space-separated. `{ref}`, `data-ref`, and level-1 chapter headings are invalid. Each question owns resources under `DIR/<id>/`; there are no shared root resources. References use `<id>/file`; only that question's `.js` and `.css` run in its frame. Question frames are served at `/f/<key>/<question-id>/`.

The aggregate of authored text, resource bytes, rendered excerpts and diagrams is bounded to 4 MiB per call. The 32-file cap includes `round.md`; question resource files count toward it. Retained complete question artifacts generated by one call are bounded to 6 MiB. Paths must resolve to regular files beneath the input directory; symlinks are refused.

Anything invalid is reported with source line numbers before the page changes. Fix the reported errors and retry.

### Draw diagrams

`::: sequence TITLE`, `::: flow TITLE` and `::: bars TITLE` blocks in Capire or Confrontare are drawn by lavagna as static SVG; no script runs and the frame's content security policy is unchanged. The title is optional and `:::` closes the block. Lavagna measures labels with the page's font and wraps them, chooses every coordinate and colour, and generates the legend; the author writes none of them.

```text
::: sequence Salvataggio interrotto
Sessione A | a.json.tmp [atomic] | a.json
Sessione A -> a.json: apre con O_TRUNC [now]
note Sessione A: crash !1 [now]
Sessione A -> a.json.tmp: scrive e fsync [atomic]
a.json --> Sessione A: JSON troncato ?2 [-atomic]
:::

::: flow Chi scrive il file
s = Sessione A
tmp = a.json.tmp [atomic]
s -> a.json: sovrascrive !1 [-atomic]
s -> tmp: scrive e fsync [atomic]
tmp --> a.json: rename [atomic]
group Disco: tmp, a.json
:::

::: bars Tempo per salvataggio
Scrittura: 4 ms !1 [now]
fsync: 5 ms +1 [atomic]
:::
```

- **Variants.** A diagram is drawn for `now`, the present state, and for each option of the question. `[now a b]` at the end of a line keeps the element only in those variants, `[-a]` keeps it in all but those, and a line without a tag is always drawn.
- **Markers.** Before the tag, `!n`, `?n` and `+n` mark a problem, a risk and a change with circled badge n, as the `45!1` excerpt mark does; `!`, `?` and `+` set only the tone. An element drawn in an option's variant and absent from `now` is drawn as a change without a marker.
- **sequence.** An optional first line `A | B [tag] | C` fixes the participants and their order; otherwise they appear in order of use. `A -> B: text` is a message, `A --> B: text` a dashed reply and `note A: text` a note on A. A line may not name a participant missing from one of its variants.
- **flow.** Lavagna lays the nodes out left to right, keeps edges out of the nodes they do not join and keeps each group's box free of other nodes. `id = Label` gives a node a short alias, which may not repeat another node's label, and a node is named by its label or alias. `A -> B: text` is an edge between two different nodes whose text is optional, `A --> B` a dashed edge and a name alone a node without edges; an edge adds the nodes it names. `group Name: A, B` boxes nodes that other lines define, and any line starting with `group ` is a group; a node belongs to at most one group and groups do not nest. The line declaring a node, alone or by alias, gives it its tag and marker; an edge may not name a node missing from one of its variants. Each variant is laid out on its own, so nodes may move between variants.
- **bars.** One `Label: value unit` per bar, values non-negative and one unit for every bar. Every variant uses the same scale.
- **Placement.** A diagram written in Capire is drawn in 01 for `now`, whatever the choice. 02 repeats each one that has a variant tag, drawn for the variant 02 shows, so choosing an option or previewing a chip redraws it; a question without Confrontare gets a 02 for these diagrams. A diagram written in Confrontare appears only in 02, drawn for the variant shown.
- **Limits.** `sequence` has at most 8 participants and 40 lines, `flow` at most 20 nodes, 30 edges and 4 groups, `bars` at most 12 bars. Titles and labels are plain text of at most 80 characters; `**bold**` and `` `code` `` are not interpreted. Characters missing from the font are allowed and drawn by the browser's fallback font with a margin. An unknown variant, participant, alias or node, or any other grammar error, is reported with its line.

## Continue a phase

Lavagna keeps the phase's questions in its question ledger, so each call after the first sends only what changes. Call elements stand outside questions, start at the beginning of a line and close with `:::`.

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

| Element | Effect |
| --- | --- |
| `::: phase TITLE` | Names the phase; valid only in the first call. |
| A new question with a body | Opens the next round. Every unsettled question of the current round moves into it, answered or not; the old round marks it moved. |
| A whole question with a known id | Replaces it in place. The thread stays; the recorded choice survives when its option id does, and a free-text answer always survives. A replaced settled question reopens in the current round, its old round marks it reopened and its decisions row is struck through. |
| A bare title | Plans a question, or updates a planned one; a question with a body is replaced only by a whole question. |
| `::: reply ID` or `::: reply` | Posts the body as an agent message in that question's discussion, closed rounds included, or in the Overview. |
| `::: settled ID [OPTION]` | Closes the question with the recorded answer, or with that option of the current version. The body is the Why of its decisions row, which also copies the title, decision, round and rejected options. Settling again replaces the decision. |
| `::: recap` | Inside a question, renders the decisions table after this call's settles. Write only risks, evidence and the confirm-or-correct options around it. |
| No element | Resumes waiting, for example after Esc. |

Elements apply in the order phase, settled, replacements, reply, new questions, recap; a reply cannot address a question new in the same call. Replies, settles and replacements stay in the current round. Any invalid element rejects the whole call, and the ledger and stored questions stay unchanged.

The current versions of all questions, rendered with their resources, are bounded to 16 MiB per phase; one agent message is bounded to 32 KiB.

## Answer on the page

The title bar shows the phase title and the delivery stage. Below it are the rail of rounds, the active question and its discussion; the footer holds the draft summary, Send to Agent and the <kbd>⌘</kbd><kbd>↩</kbd> hint.

- The rail lists the Overview first, then each round: closed rounds, the current round marked CURRENT, and the planned questions in a locked round marked "dopo Qn", which may change. A card shows whether the question is unseen, a dot for a new agent reply, its message count and its choice. A closed round keeps a struck card for a question that moved on or was reopened. The first question of the current round opens by default; clicking a card switches the center.
- 03 marks the recommended option and shows its reason. Clicking the selected option clears it; typing in the free-text card "Inserisci la tua risposta se nessuna opzione ti convince" replaces the option. A question of a closed round, or one already settled, can be reread and commented; its decision is fixed.
- Each question and the Overview have their own discussion: sent messages read "Tu", agent replies come from the call. Messages added there, and screenshots pasted or dropped on it, stay marked as drafts until Send to Agent or <kbd>⌘</kbd><kbd>↩</kbd> submits all of them with the answers of the current round's open questions. An agent reply changes only its discussion; a replaced question is redrawn alone and becomes unseen again.
- The Overview shows the decisions table; a reopened decision is struck through.
- "Espandi a tutta pagina" on a fitted block hides the rail and the discussion so the block refits to the wider column; at phone width it opens the block full screen at its native size, scrollable in both directions, under a bar of the page with "Riduci" that the question's scripts cannot hide. "Riduci", <kbd>Esc</kbd> or another question ends the expansion.
- In a window about 760 px wide the rail shrinks to question numbers and the discussion stays on the right. At phone width the rail becomes a strip of cards under the title bar and the discussion a sheet opened from the footer.
- The draft belongs to the phase and survives each call, so you can keep writing while the agent works. It is frozen only while Send awaits lavagna's reply; Send then stays disabled until the next call. A counter appears near the 32 KiB text bound.

## Collect feedback

One Send submits the current choices or free-text answers of the current round's open questions, messages and screenshots on any question, plus optional Overview messages and screenshots. Questions of closed rounds carry only messages and screenshots; their decision is already recorded. An unanswered question is not approval. PNG, JPEG, WebP and GIF screenshots are accepted by file signature, at most 10 MiB each and 8 per batch. Lavagna accepts uploaded bytes, not a URL or host path to fetch. Screenshots remain until close or the one-day sweep.

Feedback text, including free-text answers, is capped at 32 KiB per batch; the complete retained record is bounded to 48 KiB. These are admission bounds, not token budgets.

## Read the result

`round` writes exactly one JSON outcome to stdout. Its operational status line goes to stderr. A small feedback result is grouped by question:

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

Above 2 KiB, the result is deferred: choices remain visible, `messages` and `images` become counts, and a free-text answer is `true`. The full record is retained before success is returned.

```sh
lavagna feedback s-3f9543fe0510ab8e                 # deferred summary
lavagna feedback s-3f9543fe0510ab8e --question crash # one question, complete
lavagna feedback s-3f9543fe0510ab8e --overview       # Overview feedback
lavagna feedback s-3f9543fe0510ab8e --all            # full record
```

The selectors are mutually exclusive. An unknown id or a question with no feedback in that batch is an error. Read all feedback relevant to a decision and any needed images before acting; close removes these local references. A deferred receipt does not establish that the agent read every message.

## Interpret delivery status

The page reports accepted and returned stages from server and CLI events, in short form in the title bar and as a sentence in the footer, followed by "· N in bozza" while items are staged. A supported Pi session witness can report receipt and turn completion; those are delivery observations, not proof that the agent understood feedback or that the phase is settled. Without a witness the footer adds "stato in tempo reale non disponibile".

| Stage | Title bar | Footer |
| --- | --- | --- |
| Nothing sent in this call | Tocca a te | The draft summary, or "Niente in bozza" |
| Accepted by lavagna | Inviato | Ricevuto da lavagna · non ancora consegnato all’agente |
| Returned to the terminal | Consegnato al terminale | Consegnato al terminale |
| Read by the agent | L’agente lavora | Letto dall’agente · l’agente lavora |
| Turn ended or interrupted | Grilling ancora aperto, Turno interrotto | Whether the agent read the batch first |
| Uncertain | Consegna non riuscita | The sent messages are back in the draft |

Each call has its own round token and frame key, and delivery outcomes such as Interrupted and Uncertain belong to the call, so a reply-only call stays in its round.

## Recover an interrupted round

The relay keeps the page origin live during the agent's turn. If a call is interrupted, reload the page and inspect the displayed delivery state before retrying; a call with no element resumes waiting without resending content. A returned batch is not resent automatically. A running call holds the conversation lease; concurrent calls report busy.

The draft belongs to the phase, so the page recovers per question:

- A reload or a reopened tab restores the rail, the active question, threads, every question's and the Overview's draft, and seen marks. Each question's content is cached when it arrives, so any question can be reread while nothing listens.
- While the event stream is down, Send and <kbd>⌘</kbd><kbd>↩</kbd> stay disabled and drafts stay intact; the page reconnects to later calls by itself. A tab reloaded after Send but before the batch was returned shows the batch's messages as "Tu" and "Riconnessione a lavagna…" without claiming a stage.
- If a stream watched live at Accepted drops before Returned, the batch is Uncertain: its messages and screenshots return to the draft before anything written after Send, and a choice changed after Send keeps the newer value. Once the agent's ledger holds that batch, its messages appear as sent and leave the draft, so resending does not duplicate them.
- Each call reconciles the draft. A question moved into the new round keeps its draft. A rewritten question keeps a choice whose option survives and becomes unseen; otherwise the choice is cleared and its discussion notes "La tua scelta B non esiste più nella nuova versione". A settled question drops a different staged answer with the note "Chiusa con A; la tua bozza B non è stata inviata. Scrivilo qui se vuoi riaprirla."
- Tabs of one conversation share the draft; a tab of an earlier call cannot overwrite a later call's draft.
- If another process holds the port, the next call opens a new origin. The old tab is detached: read-only, with "Copia bozza" to copy all staged text grouped by question and Overview. The new origin starts with an empty draft and rebuilds rail and threads from the call.
- After an upgrade, a tab built by another lavagna reloads once. A draft the new page cannot read is offered once through "Copia bozza", then discarded.

Screenshots can be attached only until Send is accepted; text written after Send stays in the draft for the next call.

`close` is destructive and repeatable. It removes retained local files even if no browser is connected, and from a connected page it removes the phase draft, seen marks, cached content and the service worker; browser cleanup is confirmed only when that page acknowledges it.

After `close`, the one-day expiry or an upgrade, the phase starts empty. A `reply` or `settled` naming a question the ledger lacks is invalid; send the complete questions again. State written by an older lavagna starts a fresh phase on the same page origin, deletes the old phase data and notes the restart on stderr. State written by a newer lavagna makes `round` and `feedback` return an error asking for `lavagna close`, without touching anything; `close` works on every format. `lavagna feedback S --all` returns a record as its version wrote it; `--question` and `--overview` on a record older than per-question feedback are errors suggesting `--all`.
