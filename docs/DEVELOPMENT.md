# Develop lavagna

Daily use starts in [Browser rounds](rounds.md). The [component map](ARCHITECTURE.md#components) locates implementation owners; [AGENTS.md](../AGENTS.md) routes agent work to the relevant project instructions.

## Setup

Use the Go version in [`go.mod`](../go.mod). The binary uses only the Go standard library and embeds the browser assets. [`Makefile`](../Makefile) owns installation and its destination; on macOS it runs [`macos/notifier/install.sh`](../macos/notifier/install.sh), which compiles the notification helper with `swiftc`, converts `AppIcon.png` (1024×1024 PNG) to the bundle icon and signs the bundle ad hoc.

```sh
go test ./...
go build -o /tmp/lavagna .
```

The implementation uses Unix file locks and sockets. The default browser opener is macOS `open`; set `$BROWSER` for another opener. Standard-library-only does not imply Windows support.

Read [delivery policy](agents/delivery.md) before preparing a branch or publishing.

## Verification

Run `go test ./...` and build to a temporary path so verification does not replace the installed executable. Match additional checks to the changed boundary:

| Boundary | Checks |
| --- | --- |
| Question grammar, resources and excerpts | `internal/round/*_test.go` |
| Diagram grammar, font metrics and layout | `internal/diagram/*_test.go`; `BenchmarkFlowAtTheLimit` for bounded flow performance |
| Persistence, ledger and lifecycle | `internal/conversation/*_test.go` |
| Admission, feedback and frame isolation | `internal/live/*_test.go` |
| Browser presentation and recovery | `internal/live/page_test.go`, `layout_test.go`, `recovery_test.go`, `isolation_test.go` |
| CLI calls and listener handoff | Root `*_test.go` |
| Harness observations | `internal/witness/*_test.go` and fixtures in `internal/witnesstest/testdata` |

Browser tests use [`internal/cdptest`](../internal/cdptest) to drive headless Chrome and skip if Chrome or Chromium is unavailable. Check for skips before claiming browser coverage. Narrow and phone tests emulate viewports, not physical devices. CLI integration tests set `BROWSER=true` to avoid opening user tabs, and a temporary `HOME` so they never reach the installed notification helper; notification tests install a recording stand-in there. After changing the helper, check it by hand on macOS: notification text and icon, click return to the Chrome tab, denied permission and Focus. Witness fixtures run without a model session; [`cut.mjs`](../internal/witnesstest/testdata/cut.mjs) and [`record.mjs`](../internal/witnesstest/testdata/record.mjs) distinguish captured and constructed inputs.

For changes to agent-facing command input or output, run `uv run scripts/measure-context.py`. The script owns the pinned baseline, fixtures, tokenizer and regression thresholds. It counts arguments, authored stdin and both output streams, not reasoning, harness wrappers or image tokens. Tokenization is a development-only dependency.

For documentation-only changes, inspect the full Markdown diff, check local links and anchors, and verify reading triggers. Check changed commands and behavior claims against their implementation or help.

## Documentation ownership

Update the artifact responsible for a meaning and link to it elsewhere.

| Artifact | Responsibility |
| --- | --- |
| GitHub issue | Problem, scope, requirements, acceptance criteria and unresolved work decisions |
| Pull request | Change explanation, review discussion and verification of that revision |
| `docs/adr/` | Accepted architectural choices, rejected alternatives and enduring rationale |
| [`CONTEXT.md`](../CONTEXT.md) | Domain terms and meanings |
| [`AGENTS.md`](../AGENTS.md) | Repository-wide agent instructions and conditional reading triggers |
| [`Architecture`](ARCHITECTURE.md) | Component responsibilities, consistency boundaries and decision pointers |
| [`README.md`](../README.md) | Introduction, installation entry point and navigation |
| [`Browser rounds`](rounds.md) | Running a phase, feedback, delivery status and recovery |
| [`Authoring questions`](authoring.md) | Question content, diagrams, prototypes and input limits |
| [`Security`](../SECURITY.md) | Trust boundaries, data exposure, accepted risks and reporting |
| `docs/DEVELOPMENT.md` | Contributor setup, verification and documentation maintenance |
| `docs/agents/` | Project-specific tracker, labels, delivery and domain-document conventions |
| Code, tests, help and configuration | Exact schemas, algorithms, syntax and executable defaults |

Update a guide with its behavior and maintain links from the relevant entry points. Before creating an ADR, use the shared `domain-modeling` skill's necessity gate and format. Use the next number, preserve the acceptance source and add a conditional pointer in Architecture.
