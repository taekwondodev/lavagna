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

For offline reloads, the shell caches the view and every question frame on arrival, with its named resources, and reconstructs an in-memory blob document. It retains `sandbox="allow-scripts"`, embeds resource bytes as data URLs, and pins the copied scripts with CSP hashes and integrity attributes. The shell permits these local snapshot transports; the frame still cannot read the shell or make HTTP, fetch or WebSocket connections. This does not add a script opt-in or change the accepted WebRTC behavior.

The sandbox and CSP restrict storage, popups, form submission, navigation and resource loading. Round resources and `data:` images are allowed; external HTTP resource loads and fetch, beacon and WebSocket connections are blocked by the policy. This is not a guarantee that all network traffic is blocked: WebRTC is an accepted exception below. The raw HTML resource check is a parser lint, not the isolation boundary; browser enforcement is the boundary.

The CLI reads round files under [bounded path rules](docs/authoring.md#input-limits-and-errors). Code excerpts are read once from regular files under the working directory's Git root and included in the rendered frame. A round script cannot request arbitrary host files through this mechanism, but it can read content already included in its own frame.

## macOS notification helper

`Lavagna.app` is part of the trusted installation. The CLI passes it only the notification kind and the page's capability URL, as process arguments; it receives no question content, answers, screenshots or transcript. Notification text is fixed, and the URL appears neither in it nor in diagnostics. The helper accepts only `http://127.0.0.1:PORT/s/…` URLs and passes the URL to its AppleScript as an Apple event parameter, never as script source.

A click searches Chrome's open tabs for that URL. The Chrome automation permission this requires lets the helper read and control every Chrome window, not only lavagna's tab; macOS grants it to the helper, not to the CLI. Denying it leaves notifications working and makes a click open the page in the default browser instead.

## Retained data

Conversation directories are private to the account (mode 0700), with retained files at mode 0600. Question artifacts, screenshots and exact feedback remain there for phase continuity and selective CLI reading. Feedback artifacts are not HTTP resources. [Architecture](docs/ARCHITECTURE.md#conversation-ownership) describes ownership and publication; [Browser rounds](docs/rounds.md#close-or-restart) describes retention, expiry and close.

Browser storage is a separate copy. A disconnected browser may retain its offline content after local files have been deleted; cleanup is confirmed only when a connected page acknowledges it. File permissions and deletion are not secure erasure and do not remove copies already present in a harness transcript.

## Accepted residual risk: WebRTC

Chrome permits WebRTC traffic despite the frame's CSP, including `connect-src 'none'`. A round script can initiate UDP/STUN traffic to a reachable destination. JavaScript-level blocking is not a reliable boundary because alternate document and frame contexts can bypass it.

Consequences include exposing the user's network address to a destination and a possible channel for transmitting information accessible to the script. That information includes the round's rendered text, data and code excerpts. The frame's lack of a shell token does **not** mean it contains no sensitive material.

The maintainer explicitly accepted this risk on 2026-10-05 and chose to keep the current behavior. Lavagna adds no script opt-in, per-prototype consent prompt or WebRTC mitigation. This accepts the limit rather than claiming to eliminate it. The decision narrows the earlier blanket statement that content cannot make network requests in the [isolation decision](https://github.com/taekwondodev/dev/issues/95).

## Accepted residual risk: unsent choice visible to round scripts

The option state exposed through the [shell and content boundary](#shell-and-content-boundary) is visible before Send. A round script could transmit that unsent choice through the accepted WebRTC egress above. [ADR 0001](docs/adr/0001-frame-receives-selected-option.md) owns the decision to expose it and the rejected alternatives.

## Accepted residual risk: round scripts can expand on the user's tap

Espandi lives in the content frame, so the shell accepts `expand` from any user gesture inside the active frame. A round script can therefore expand the page again whenever the user taps its content, even right after Riduci. It cannot expand the page without such a tap, and the shell's own Riduci and controls stay usable without touching the frame. The [frame decision](https://github.com/taekwondodev/lavagna/issues/16#issuecomment-6033567938) records the rejected shell-button alternative. The maintainer accepted this risk on 2026-10-07.

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
