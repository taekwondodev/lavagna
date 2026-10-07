# Security

Lavagna is a local CLI for browser rounds in an agent conversation. Its trust model is a personal, single-user machine, not a hostile multi-user service or a general-purpose sandbox for untrusted web applications. Read this before presenting code or other sensitive material in a round.

## Trusted base

- The account running lavagna, including the machine's trusted administrator accounts, is the security boundary. Processes inside it can already read lavagna's state, the agent transcript and the browser profile.
- The installed lavagna binary, its embedded shell and frame helper, the browser, and the agent that prepares rounds are trusted parts of the workflow.
- The CLI runs with the invoking account's permissions. The browser frame's isolation does not sandbox the CLI or the agent's other tools.
- Round-supplied JavaScript runs without a separate opt-in or confirmation. It is isolated from the feedback shell, but it must not be treated as network-isolated code.

## Shell and content boundary

Lavagna's shell owns decision controls, threads, the draft, send, the conversation capability and the [round token](CONTEXT.md). Agent-supplied explanations and prototypes render in an opaque-origin `<iframe sandbox="allow-scripts">`, one for the active question, destroyed on every switch; frame responses repeat the sandbox in Content Security Policy (CSP). The shell shows titles, leads, options and messages as text nodes and never parses agent HTML.

The frame has its own [frame key](CONTEXT.md), not the shell capability or round token. The shell accepts frame messages only from the active frame's window and acts only on `layout` and `expand`, so a forged message changes presentation at most. It honors `expand` only while the frame holds focus during a user gesture, and at phone width keeps its own Riduci above the expanded frame, so a round script cannot trap the user in an expansion. It sends the frame only `option`, the selected option id and whether a free-text answer exists, and `expanded`, the expansion state; never the free text, messages, screenshots, capability or round token. The feedback endpoint requires POST, the shell's Origin and the round token. Round scripts cannot read the shell DOM or its draft, obtain its token through that DOM, or submit valid feedback on the user's behalf.

For offline reloads, the shell caches each question frame it shows, with its named resources, and reconstructs an in-memory blob document. It retains `sandbox="allow-scripts"`, embeds resource bytes as data URLs, and pins the copied scripts with CSP hashes and integrity attributes. The shell permits these local snapshot transports; the frame still cannot read the shell or make HTTP, fetch or WebSocket connections. This does not add a script opt-in or change the accepted WebRTC behavior.

The sandbox and CSP restrict storage, popups, form submission, navigation and resource loading. Round resources and `data:` images are allowed; external HTTP resource loads and fetch, beacon and WebSocket connections are blocked by the policy. This is not a guarantee that all network traffic is blocked: WebRTC is an accepted exception below. The raw HTML resource check is a parser lint, not the isolation boundary; browser enforcement is the boundary.

The CLI reads round files under bounded path rules. Code excerpts are read once from regular files under the working directory's Git root and included in the rendered frame. A round script cannot request arbitrary host files through this mechanism, but it can read content already included in its own frame.

Versioned question artifacts, uploaded screenshots and exact feedback are retained in the private conversation directory for phase continuity and selective CLI reading. Feedback artifacts are not HTTP resources. They are scoped to the current conversation, deleted by `close`, or swept after one day of inactivity once their lease can be acquired. Source directories are not deleted. Browser storage is a separate copy: `close` can confirm its cleanup only when a connected page acknowledges it. A disconnected browser may retain its offline copy. File permissions and deletion are not secure erasure and do not remove copies already present in a harness transcript.

## Accepted residual risk: WebRTC

Chrome permits WebRTC traffic despite the frame's CSP, including `connect-src 'none'`. A round script can initiate UDP/STUN traffic to a reachable destination. JavaScript-level blocking is not a reliable boundary; the implementation investigation bypassed it through alternate document and frame contexts.

Consequences include exposing the user's network address to a destination and a possible channel for transmitting information accessible to the script. That information includes the round's rendered text, data and code excerpts. The frame's lack of a shell token does **not** mean it contains no sensitive material.

A review probe reproduced a STUN request from a round script using the actual sandboxed frame, with the destination confined to loopback. It demonstrated network egress; it did not separately demonstrate exfiltration of repository code.

The maintainer explicitly accepted this risk on 2026-10-05 and chose to keep the current behavior. Lavagna adds no script opt-in, per-prototype consent prompt or WebRTC mitigation. This accepts the limit rather than claiming to eliminate it. The decision narrows the earlier blanket statement that content cannot make network requests in the [isolation decision](https://github.com/taekwondodev/dev/issues/95).

## Accepted residual risk: unsent choice visible to round scripts

So that 02 follows the choice, the shell tells the active question's content frame which option the user selected in 03, or that a free-text answer exists, before Send. Round scripts can read it and, through the accepted WebRTC egress above, could transmit it before the user sends feedback. The frame never receives the free text, thread messages, screenshots, the shell capability or the round token. See [ADR 0001](docs/adr/0001-frame-receives-selected-option.md).

## Accepted residual risk: round scripts can expand on the user's tap

Espandi lives in the content frame, so the shell accepts `expand` from any user gesture inside the active frame. A round script can therefore expand the page again whenever the user taps its content, even right after Riduci. It cannot expand the page without such a tap, and the shell's own Riduci and controls stay usable without touching the frame. Preventing it would move Espandi into the shell, an alternative rejected in [How does the content frame show 01 and 02 of the active question?](https://github.com/taekwondodev/lavagna/issues/16#issuecomment-6033567938). The maintainer accepted this risk on 2026-10-07.

## Accepted residual risk: local port squatting

A conversation reuses a loopback origin. During gaps when no lavagna process holds its port, another local process can bind it. The reconnecting page can present its capability URL to that process, and the page does not cryptographically authenticate the server. A process impersonating the server is outside the protection of the feedback endpoint's client-side identity checks.

This is accepted under the trusted-account boundary, not prevented by loopback addressing or file permissions. The capability URL also appears in the terminal output and agent transcript. Port-collision handling is a robustness measure, not server authentication. See the [reconnect decision](https://github.com/taekwondodev/dev/issues/100).

## Outside the protection

- Isolation does not establish that the content is truthful or harmless. A prototype can display misleading information even though it cannot submit valid feedback.
- Content intentionally included in a round is available to that round's scripts. Lavagna does not classify or redact sensitive excerpts before rendering them.
- File permissions and browser isolation do not protect against trusted-account processes, administrator access, a compromised browser or a compromised lavagna installation.
- The verified browser behavior is not a universal guarantee for every browser or future release.

## Reporting

Report security concerns in the project's [GitHub issue tracker](https://github.com/taekwondodev/lavagna/issues). Do not include secrets, capability URLs or sensitive round content in a report.
