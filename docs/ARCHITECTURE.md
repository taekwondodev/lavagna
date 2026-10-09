# Architecture

Lavagna coordinates a browser page with one agent conversation. [CONTEXT.md](../CONTEXT.md) defines the domain terms. [Browser rounds](rounds.md) describes operation and recovery; [Security](../SECURITY.md) owns trust boundaries and accepted risks.

## Components

| Owner | Responsibility | Reference |
| --- | --- | --- |
| [`main.go`](../main.go) | Dispatch public commands and the internal relay, Hermes hook and hook-registration entry points | [Commands](rounds.md#present-and-close) |
| [`internal/harness`](../internal/harness) | Recognize the invoking harness: its conversation identity, the owner that settles nested harnesses, the record its witness follows, and its call deadline | [Bind a conversation](rounds.md#bind-a-conversation) |
| [`internal/round`](../internal/round) | Load bounded question inputs, parse authored content and render per-question documents | [Authoring](authoring.md) |
| [`internal/diagram`](../internal/diagram) | Parse diagrams, measure labels with the embedded font and draw static SVG variants for `internal/round` | [Diagrams](authoring.md#draw-diagrams) |
| [`internal/conversation`](../internal/conversation) | Derive the key from a harness identity, serialize access, persist the ledger and artifacts, and remove expired state | [Conversation ownership](#conversation-ownership) |
| [`internal/live`](../internal/live) | Coordinate calls, serve the page, admit feedback and hand the listener between a call and its relay | [Call and feedback flow](#call-and-feedback-flow) |
| [`internal/page`](../internal/page) | Embed the trusted shell and frame helper; manage presentation, drafts and offline snapshots in the browser | [Browser continuity](#browser-continuity) |
| [`macos/notifier`](../macos/notifier) | macOS helper app: post fixed-text notifications and bring the conversation's Chrome tab forward on click | [Notifications](rounds.md#get-notified-on-macos) |
| [`internal/witness`](../internal/witness) | Observe Pi sessions, Claude Code transcripts and Hermes hook events for receipt and turn completion without writing any harness record | [Delivery status](rounds.md#interpret-delivery-status) |

## Conversation ownership

The conversation key is a digest of the harness identity that [`internal/harness`](../internal/harness) resolves; Pi and `LAVAGNA_SESSION` identities keep the encoding they had before other harnesses were recognized. Pages never receive raw session values. Each conversation has a private cache directory containing:

| Data | Role |
| --- | --- |
| `state.json` | Commit point for the question ledger, page origin and call lifecycle |
| `artifacts/question/<id>-<version>.json` | Immutable rendered question versions with their complete resources; planned questions have no artifact |
| `artifacts/feedback/` | Retained batches for selective CLI reading |
| `images/` | Uploaded screenshots |
| `events.jsonl` | Hermes hook observations: submission ids and turn outcomes, created only when a witness follows the conversation |
| `lock` | Lease shared by calls, close and expiry cleanup |

Exact state fields and format checks belong to [`conversation.go`](../internal/conversation/conversation.go); artifact publication belongs to [`artifacts.go`](../internal/conversation/artifacts.go). [Security](../SECURITY.md#retained-data) covers permissions and exposure, and [recovery](rounds.md#recover-an-interrupted-round) covers format changes and deletion visible to users.

A non-blocking file lock serializes access to one conversation. Artifact reads and writes hold that lease. `close` retains the lock file so racing calls cannot lock different inodes; the kernel releases the lease on process death. Acquiring a lease refreshes activity. A sweep rechecks expiry after acquiring another conversation's lease.

## Call and feedback flow

[`phase.go`](../internal/live/phase.go) coordinates a call's commit: validate all elements against the ledger, write new immutable question versions, replace `state.json` with one rename, then prune unreferenced question artifacts. This ordering leaves the prior ledger intact on validation failure and lets a later commit remove artifacts orphaned by a crash.

Two pure transition owners keep content and delivery separate:

- [`ledger.go`](../internal/conversation/ledger.go) applies call elements and accepted batches. It owns question transitions, recorded answers and submission deduplication. Read [ADR 0002](adr/0002-ledger-apply-stays-one-transition.md) before splitting `Apply` or adding elements.
- [`lifecycle.go`](../internal/conversation/lifecycle.go) records call start, acceptance and return. Delivery is keyed by call, not round: a reply-only call does not advance the round. Returned is recorded only after the result write succeeds.

After serving the page and before waiting for feedback, [`notify.go`](../internal/live/notify.go) classifies the committed call against the prior ledger and asks the helper to post at most one notification. It passes only the notification kind and the page URL; a failure is reported on stderr and never changes the call's outcome. The relay and the witness do not notify.

[`gate.go`](../internal/live/gate.go) owns the send admission decision table. The live call persists admitted feedback before returning a successful result; the CLI resolves screenshot IDs to host paths, while browser views expose only IDs. [Feedback results](rounds.md#read-the-result) describes inline and deferred reads.

## Listener handoff and witness

After returning feedback, or pausing at the deadline its harness imposes, the call starts an internal relay to hold the page origin during the agent's turn, whatever the harness. A paused call's relay serves the page with admission closed until the next call takes the origin. The relay has no inherited stdio and remains in the call's process group. It watches the parent of the group leader rather than the temporary call shell.

The listener passes to the relay as an inherited descriptor and to a later call over `relay.sock` using `SCM_RIGHTS`. Preserve these constraints when changing [`relay.go`](../internal/live/relay.go) or [`owner.go`](../internal/live/owner.go):

- Duplicate without `File`: non-blocking mode belongs to the shared file description, and a blocking accept can take connections after ownership moves.
- Close the sender's copies only after the receiver acknowledges possession; closing during transfer can lose the socket on macOS.
- Each holder stops accepting and finishes requests it already accepted, one request per connection. `http.Server.Shutdown` can drop requests read after shutdown begins.

The witness reads the harness's record from the return offset and matches the submission ID in a tool result: Pi's session file, Claude Code's transcript, or for Hermes the `events.jsonl` that its shell hooks append through `lavagna hermes-hook`. Hermes's own store does not distinguish an interrupted turn, so its hooks report instead. The witness reports only supported observations; unknown evidence ends observation without inventing a receipt. [`internal/witness`](../internal/witness) owns accepted record shapes. The [delivery guide](rounds.md#interpret-delivery-status) owns the meaning of those observations, including the limit of a deferred receipt.

## Browser continuity

The shell owns decisions, threads and the phase draft. It creates a content frame for the active question, destroys it on a switch, and preserves it across calls only when that question's version stays unchanged. [Authoring](authoring.md) describes chapter presentation and the script-facing API; [Security](../SECURITY.md#shell-and-content-boundary) defines what can cross the frame boundary. Read [ADR 0001](adr/0001-frame-receives-selected-option.md) before exposing more shell state to the frame.

The server's ledger is the source of sent history; browser storage owns unsent work. A versioned draft under the capability path separates conversations even when a port is reused. Reconciliation uses question versions, call numbers and submission IDs to preserve compatible drafts, prevent stale-tab overwrites and remove restored messages once the ledger contains them. [Recovery](rounds.md#recover-an-interrupted-round) owns the user-visible outcomes.

A capability-scoped service worker caches the shell, using the network when available. It never intercepts send or the event stream. The shell caches each view and every question's document and resources on arrival. Offline frames are reconstructed as in-memory documents because an opaque frame cannot rely on service-worker subresource delivery. Their isolation policy is in [Security](../SECURITY.md#shell-and-content-boundary).

Close waits for pending browser cache writes before deleting storage. A capability- and Origin-protected acknowledgement distinguishes completed browser cleanup from a closed event being written. Local file deletion does not depend on that acknowledgement.

## Decision references

Read the record for the boundary being reconsidered.

| Record | Read before changing |
| --- | --- |
| [ADR 0001](adr/0001-frame-receives-selected-option.md) | Shell state exposed to content scripts |
| [ADR 0002](adr/0002-ledger-apply-stays-one-transition.md) | The ordered, all-or-nothing ledger transition |
| [Isolation decision](https://github.com/taekwondodev/dev/issues/95) | Content isolation, subject to the [accepted WebRTC risk](../SECURITY.md#accepted-residual-risk-webrtc) |
| [Reconnect decision](https://github.com/taekwondodev/dev/issues/100) | Origin reuse and the trusted-account boundary |

The original [requirements](https://github.com/taekwondodev/dev/issues/15) and [design route](https://github.com/taekwondodev/dev/issues/89) remain historical records in `taekwondodev/dev`. New work uses this repository's [tracker](agents/issue-tracker.md). Follow [documentation ownership](DEVELOPMENT.md#documentation-ownership) when recording a new decision.
