# Architecture

Lavagna is a local CLI coordinating a browser page with one agent conversation. [Domain context](../CONTEXT.md) defines its vocabulary; [Rounds](rounds.md) owns commands, limits and recovery; [Security](../SECURITY.md) owns trust boundaries and accepted risks.

## Components

| Component | Responsibility | Behavior reference |
| --- | --- | --- |
| [`main.go`](../main.go) | Dispatch public commands and the internal relay entry point | [Commands](rounds.md#present-and-close) |
| [`internal/round`](../internal/round) | Load bounded round inputs, parse the grammar and compose immutable content snapshots | [Authoring](rounds.md#author-a-round) |
| [`internal/conversation`](../internal/conversation) | Bind identity, persist private state and artifacts, serialize calls and sweep expired state | [Conversation and recovery](rounds.md#recover-an-interrupted-round) |
| [`internal/live`](../internal/live) | Serve the loopback origin, admit feedback, store screenshot uploads and hand the listener between calls and relay | [Feedback](rounds.md#collect-feedback), [Security](../SECURITY.md) |
| [`internal/page`](../internal/page) | Embed and render the shell and content frame; retain browser drafts and offline snapshots | [Recovery](rounds.md#recover-an-interrupted-round) |
| [`internal/witness`](../internal/witness) | Observe supported Pi session entries without writing the transcript | [Delivery status](rounds.md#interpret-delivery-status) |

Test support and verification boundaries are in [Development](DEVELOPMENT.md#verification).

## Conversation ownership

The conversation key is a digest of the harness identity; pages never see the raw session values. The user cache directory holds `lavagna/<key>/state.json` with mode 0600 inside a mode-0700 directory. Lifecycle state retains the loopback origin, its 256-bit page capability, round counter, live lifecycle and authored anchors. Immutable content snapshots and full feedback records are separate private artifacts; uploaded screenshots remain files under `images/`. Retained files use mode 0600. Artifact creation and reads hold the conversation lease, so `close` and expiry cannot race a partial read or publication.

A non-blocking `flock` serializes CLI calls for one conversation. `close` keeps the lock file while deleting other conversation data, so racing calls cannot lock different inodes. The kernel releases the lease on process death. Taking a lease touches the conversation directory; a sweep removes another conversation only if it is still expired after acquiring its lease.

[`lifecycle.go`](../internal/conversation/lifecycle.go) owns the pure transition function for round started, batch accepted and batch returned. A new round turns a previous live round into an Interrupted or Uncertain outcome according to whether a batch was accepted. Returned is recorded only after the result write succeeds. [The guide](rounds.md#interpret-delivery-status) explains what those observations mean to the user.

## Listener handoff and witness

After returning feedback, the call starts the internal relay to keep the page origin live during the agent's turn. The relay inherits no stdio but remains in the call's process group so interrupting that group kills it too. It watches the parent of the call's process-group leader, not the temporary call shell, checking PID, start time and non-zombie state with `ps` every 250 ms.

The listener moves to the relay as an inherited descriptor, then to the next `round` or `close` over `relay.sock` using `SCM_RIGHTS`. Three details protect requests across a handoff:

- Duplicate the descriptor without `File`: non-blocking mode belongs to the shared file description, and a blocking accept can take connections after ownership moves.
- Close the sender's copies only after the receiver acknowledges possession; closing during transfer can lose the socket on macOS.
- Each holder stops accepting and answers requests it already accepted itself, one request per connection; `http.Server.Shutdown` can drop requests read after shutdown begins.

Without a relay, the next call rebinds the recorded port. The public fallback and collision behavior is in [Rounds](rounds.md#recover-an-interrupted-round).

The witness reads `$PI_SESSION_FILE` from the offset where the call returned. A tool result carrying the batch's submission ID establishes Received. A `stop` or `aborted` assistant message establishes Not read before receipt, retaining whether the turn ended or was interrupted. After receipt, `stop` establishes Turn ended and `aborted` establishes Turn interrupted; neither establishes frontier closure. Unknown evidence ends observation without inventing a receipt. Before stopping, the relay gives the page up to two seconds to connect so an immediately ended turn can still be reported. Exact accepted session shapes belong to [`internal/witness`](../internal/witness), not a second schema in this guide.

## Feedback and rendering

The pure send gate distinguishes acceptance, duplicate receipt, answered, stale and foreign submissions. [`internal/live/gate.go`](../internal/live/gate.go) owns the exact decision table; [Security](../SECURITY.md#shell-and-content-boundary) owns request authorization and frame isolation.

Capire and Confrontare render in the content frame. Decidere, the editor and send stay in the shell. A frame helper reports height and proposes anchors through `postMessage`. The shell accepts proposals only from its frame window, while picking, and for anchors defined by the round. The server retains authored anchors across rounds for draft recovery. During picking and for half a second afterward, the helper intercepts pointer, keyboard and form events before prototype scripts see them. Font responses allow cross-origin access because the frame's origin is opaque.

The live CLI persists admitted feedback before returning a successful result. Small records are returned inline; large records return a bounded deferred result and remain accessible through selective CLI reads. The submission ID still identifies the witness receipt; a deferred receipt does not establish that the agent has read every comment. Content reuse supplies the original rendered frame and resource bytes, while new decisions are parsed independently. This avoids source rereads, inherited choices and a second patch grammar.

Screenshot uploads are stored by the live server; batches reference them by ID and the CLI result resolves them to local paths. The [round guide](rounds.md#collect-feedback) owns supported formats, size limits and retention.

## Browser continuity

A service worker scoped to the capability path precaches the shell, answers network-first and uses cache only when nothing listens. It never intercepts the event stream or send. The shell caches the rich document and named resources as well. An opaque sandboxed frame cannot rely on service-worker subresource delivery, so offline restoration builds an in-memory document with embedded resources. [Security](../SECURITY.md#shell-and-content-boundary) owns the snapshot's isolation and script policy.

The tab's `localStorage` keeps the displayed round and draft under the capability path, separating conversations even if a port is reused. Tabs write only on draft changes and adopt each other's writes. A changed draft receives a new submission ID. Delivery stages move forward despite races between the event stream and send reply. The page reconnects every 250 ms so a later call can attach before opening another tab. Closing destroys the content frame and in-memory feedback, waits for outstanding cache writes, then removes browser storage. A capability- and Origin-protected acknowledgement distinguishes cleanup completion from merely writing a closed event. An offline browser cannot acknowledge deletion. User-facing resend and closing behavior belongs to [Recovery](rounds.md#recover-an-interrupted-round).

## Decision references

These existing project records remain in `taekwondodev/dev`; their links are historical context, not a policy to file new lavagna work there.

| Record | Subject |
| --- | --- |
| [dev#15](https://github.com/taekwondodev/dev/issues/15) | Original requirements |
| [dev#89](https://github.com/taekwondodev/dev/issues/89) | Design route |
| [dev#95](https://github.com/taekwondodev/dev/issues/95) | Content isolation decision, qualified by the [accepted WebRTC risk](../SECURITY.md#accepted-residual-risk-webrtc) |
| [dev#100](https://github.com/taekwondodev/dev/issues/100) | Reconnect and trusted-account boundary |

Local decision records live in `docs/adr/`. Add accepted decisions only through the [documentation ownership rules](DEVELOPMENT.md#documentation-ownership), then link them here.

| Record | Subject |
| --- | --- |
| [ADR 0001](adr/0001-frame-receives-selected-option.md) | The content frame receives the selected option before Send |
