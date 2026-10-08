# Authoring questions

Write question content in `round.md`, then use [Browser rounds](rounds.md) to present it and continue the phase. `lavagna round --help` gives a minimal example; `lavagna round --help grammar` is the complete syntax reference. Read [Security](../SECURITY.md) before including sensitive excerpts or scripted prototypes.

## Author questions

A level-1 title starts a question and requires an id. Several questions can be sent in one call. A title with no body plans a question; a later call supplies its complete contents with the same id.

```markdown
# What should happen if a session crashes? {id="crash"}
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

# What should be retained? {id="retention" after="crash"}
```

Use stable ids so later calls can address the same question. `after` names known questions without cycles. A complete question needs `## Decidere` with 2–6 options; Capire and Confrontare are optional and precede it. A free-text answer is always available without authored syntax. Use `{recommended}` on at most one option and an indented `=> consequence` for its effect; a paragraph after the options explains the recommendation. The option id `now` is reserved for diagram states.

The shell displays the title, lead and Decidere as 03, as text with `` `code` `` and `**bold**`. Capire and Confrontare become 01 and 02 in the content frame. Empty chapters are hidden; a question with neither has no frame.

02 starts with one Anteprima chip per option and the effect line of the variant shown. A chip preview takes precedence over the selected option, then the recommended option is the fallback. A free-text answer shows the recommended variant with a note that the agent draws it next turn. Previewing does not change the answer.

## Include code excerpts

`::: excerpt path:start-end 45!1` includes repository lines and marks line 45 as a problem with badge 1. Separate multiple marks with spaces and close the block with `:::`. Paths resolve from the working directory's Git root; the excerpt must be a regular file within that root.

Excerpts use a dark background and lexical syntax colouring selected by extension or filename. [`highlight.go`](../internal/round/highlight.go) owns the supported language list; other files remain uncoloured. Colouring starts at the excerpt's first line, so a range beginning inside a block comment or multi-line string is coloured as code. Include enough context to avoid misleading highlighting.

## Include prototypes

A raw HTML block is a run of consecutive lines starting with a tag. It renders inside `.raw > .raw-stage`. Give a screen its native width: a block wider than the column is fitted and shows its width and scale. Below 85 % it offers Espandi. [Page controls](rounds.md#answer-on-the-page) describes expansion and narrow layouts.

For directory input, each question owns resources under `DIR/<id>/`; there are no shared root resources. References use `<id>/file`. Only that question's `.js` and `.css` run in its frame. Switching questions destroys the frame, so prototype-local state starts over on return; the shell retains the answer.

Scripts and CSS can follow the choice through the frame's `<html>` attributes:

| Attribute | Value |
| --- | --- |
| `data-option` | Selected option id; absent when none |
| `data-free` | `true` when a free-text answer exists |
| `data-variant` | Option currently shown in 02 |

`document` receives `lavagna:option` with `detail: {option, free, variant}` on load and each change. Use that API rather than reading the shell. The [frame boundary](../SECURITY.md#shell-and-content-boundary) defines what is exposed and the risk of exposing an unsent choice.

## Draw diagrams

Use `::: sequence TITLE`, `::: flow TITLE` or `::: bars TITLE` in Capire or Confrontare, closed by `:::`. The title is optional. Lavagna generates static SVG, measures and wraps labels with the page font, chooses coordinates and colours, and generates the legend. No round script is needed.

```text
::: sequence Interrupted save
Session A | a.json.tmp [atomic] | a.json
Session A -> a.json: opens with O_TRUNC [now]
note Session A: crash !1 [now]
Session A -> a.json.tmp: writes and fsyncs [atomic]
a.json --> Session A: truncated JSON ?2 [-atomic]
:::

::: flow File writers
s = Session A
tmp = a.json.tmp [atomic]
s -> a.json: overwrites !1 [-atomic]
s -> tmp: writes and fsyncs [atomic]
tmp --> a.json: rename [atomic]
group Disk: tmp, a.json
:::

::: bars Save duration
Write: 4 ms !1 [now]
fsync: 5 ms +1 [atomic]
:::
```

### Variants and placement

A diagram has a `now` variant for the present state and one for each question option. A trailing `[now a b]` keeps a line in those variants; `[-a]` keeps it in all except those; no tag means always present. References must remain valid in every variant where they appear.

Before the tag, `!n`, `?n` and `+n` mark a problem, risk or change with a circled badge; omit the number for tone only. An element present in an option variant but absent from `now` is automatically drawn as a change.

In Capire, a diagram shows `now` regardless of the choice. A diagram with variant tags also appears in 02, creating that chapter if needed. In Confrontare, a diagram appears only in 02. The variant follows the preview or choice described under [Author questions](#author-questions).

### Diagram kinds

| Kind | Authoring model |
| --- | --- |
| `sequence` | Optional first line lists participants in order, as `A \| B [tag] \| C`; otherwise order follows first use. `A -> B: text` sends a message, `A --> B: text` is a dashed reply, and `note A: text` annotates a participant. |
| `flow` | `id = Label` declares an alias; use label or alias in edges. `A -> B: text` links distinct nodes, with optional text; `A --> B` is dashed. An edge adds its nodes; a name alone declares an unconnected node. `group Name: A, B` groups nodes defined elsewhere. A node belongs to at most one group and groups do not nest. |
| `bars` | One `Label: value unit` per bar, with non-negative values and a shared unit. All variants use the same scale. |

Flow aliases cannot repeat another node's label. A node declaration owns its tag and marker; any line starting with `group ` is a group declaration. Flow layout runs separately for each variant, so nodes can move. It proceeds left to right, keeping edges out of unrelated nodes and group boxes free of unrelated nodes.

Titles and labels are plain text, without Markdown emphasis. Characters missing from the embedded font use browser fallback with a margin. The grammar help lists diagram limits and validation rules.

## Input limits and errors

| Scope | Bound |
| --- | --- |
| One call's authored text, resources, rendered excerpts and diagrams | 4 MiB |
| Directory input | 32 files including `round.md` |
| Question artifacts generated by one call | 6 MiB |
| Current rendered question versions and resources retained in a phase | 16 MiB |

Resource paths must resolve to regular files beneath the input directory; symlinks are refused. Exact title, id and diagram limits are in `lavagna round --help grammar`. `{ref}`, `data-ref` and level-1 chapter headings are unsupported; feedback belongs to questions rather than anchors.

Invalid input is reported with source line numbers before the page changes. Fix the errors and retry. The [continued-call guide](rounds.md#continue-a-phase) explains replacement, replies, settling and recaps; these operations do not require resending unchanged questions.
