# Develop lavagna

This guide is for changing lavagna itself. Daily operation starts in the [README](../README.md) and [round guide](rounds.md).

## Setup

Use the Go version declared in [`go.mod`](../go.mod). Lavagna uses only the Go standard library; the page, styles and fonts are embedded in the binary. [`Makefile`](../Makefile) owns the installation command and destination.

```sh
go test ./...
go build -o /tmp/lavagna .
```

The implementation uses Unix file locks and sockets; the default browser opener is macOS `open`. Use `$BROWSER` for another opener. Do not infer Windows support from the absence of external Go dependencies.

## Find the owner

[Architecture](ARCHITECTURE.md#components) maps source areas to behavior guides. [CONTEXT.md](../CONTEXT.md) owns vocabulary, and [AGENTS.md](../AGENTS.md#conditional-references) owns conditional reading instructions. Read [delivery policy](agents/delivery.md) before preparing implementation or publication.

## Verification

Run `go test ./...` for the existing unit, CLI and browser integration checks, and `go build -o /tmp/lavagna .` to build without replacing the installed executable. Match additional checks to the changed boundary.

| Boundary | Existing evidence |
| --- | --- |
| Round grammar, directory limits and repository excerpts | `internal/round/*_test.go` |
| Conversation locking, sweep and lifecycle | `internal/conversation/*_test.go` |
| Admission, screenshot uploads and frame isolation | `internal/live/*_test.go` |
| Command results and concurrent calls | `cli_test.go` |
| Question rendering, frame resource isolation, send admission and grouped feedback | `internal/round/questions_test.go`, `internal/live/server_test.go`, `internal/live/feedback_test.go`, `internal/live/phase_test.go` |
| Relay lifetime, listener handoff and delivery receipts | `relay_test.go`, `internal/witness/*_test.go` |

The existing browser tests bound to the previous round page were removed for the per-question transition; follow-up page tickets replace them. The current Go checks exercise question parsing, question-scoped frame routes, send validation, retained outcome reads and the CLI-to-send flow, but do not establish browser interaction coverage. [`internal/cdptest`](../internal/cdptest) remains available for the replacement page tests. CLI integration tests use `BROWSER=true`, so no tab opens in the user's browser.

[`internal/witnesstest/testdata`](../internal/witnesstest/testdata) holds the witness fixtures. `answered`, `next-round` and `close` were cut from a real Pi 1.0.3 session with `cut.mjs`; `unread`, `retry` and `unrecognized` cover cases that run did not produce, written through Pi's `SessionManager` with modelled message bodies by `record.mjs`. The checked-in fixtures are consumed without launching a model session.

For documentation-only work, inspect the full Markdown diff, verify local links and reading triggers, and confirm any documented commands or behavior against their owning source. Keep run-specific results in the issue or PR rather than this reusable guide.

For agent-context changes, run `uv run scripts/measure-context.py`. This optional tool builds the pinned baseline (`main` before per-question rounds) and the current checkout, drives local calls with fixed inputs, then counts the command line, authored stdin and both output streams using the pinned `tiktoken` package and `o200k_base` encoding. It normalizes random capability URLs and ports only. The main fixture replays one grilling phase, the five-call walk of the call model, with questions of realistic size and the same simulated batches in both formats; on the baseline every turn is a complete round, because each one changes content. It counts `round --help` once per phase and exits non-zero when the phase exceeds 50 % of the baseline tokens, the reply-only call exceeds 10 % of the baseline round it replaces, or the first call exceeds 110 % of the baseline first round. The other fixtures measure help, a round with small feedback, and large deferred feedback read one question at a time or whole. It reports tokens separately from bytes; it does not estimate reasoning, harness wrappers, image tokens or every model's tokenizer. Tokenization is a development-only dependency, not part of the binary.

## Documentation ownership

| Artifact | Owns |
| --- | --- |
| GitHub issue | Problem, scope, requirements, acceptance criteria and unresolved decisions for a piece of work |
| Pull request | Proposed implementation, review discussion and verification evidence for that change |
| `docs/adr/` when needed | Accepted architectural decisions, alternatives and enduring rationale |
| [`CONTEXT.md`](../CONTEXT.md) | Domain terms and meanings |
| [`AGENTS.md`](../AGENTS.md) | Repository-wide agent instructions and conditional reading triggers |
| [`docs/ARCHITECTURE.md`](ARCHITECTURE.md) | Current component responsibilities and decision-record index |
| [`README.md`](../README.md) | Project introduction, installation and entry points |
| Feature guide, currently [`docs/rounds.md`](rounds.md) | How to use a capability, observable behavior, limits and recovery |
| [`SECURITY.md`](../SECURITY.md) | Trust boundaries, accepted risks and reporting |
| `docs/DEVELOPMENT.md` | Contributor setup, checks and documentation maintenance |
| [`docs/agents/delivery.md`](agents/delivery.md) | Repository delivery route and target |
| Code, tests and configuration | Exact APIs, algorithms, schemas and executable defaults |

Update the existing owner instead of copying its meaning. Use links for cross-cutting constraints. A guide explains behavior; code and tests define its exact implementation; the issue states the requested change; the PR records its verification.

When adding a capability, link its guide from the component map and add a conditional reading trigger in `AGENTS.md`. Organize guides around user tasks, not implementation walkthroughs or test plans. Keep shared development procedures in the external workflow library.

Use the shared `domain-modeling` skill's ADR necessity gate and format before creating a record. Create `docs/adr/` only for a decision that passes that gate. Use the next number, link the accepted decision and add an architecture pointer. Preserve existing historical decision links rather than inventing retrospective approvals.
