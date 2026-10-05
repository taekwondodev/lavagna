# Security

Lavagna is a local CLI for browser rounds in an agent conversation. Its trust model is a personal, single-user machine, not a hostile multi-user service or a general-purpose sandbox for untrusted web applications. Read this before presenting code or other sensitive material in a round.

## Trusted base

- The account running lavagna, including the machine's trusted administrator accounts, is the security boundary. Processes inside it can already read lavagna's state, the agent transcript and the browser profile.
- The installed lavagna binary, its embedded shell and frame helper, the browser, and the agent that prepares rounds are trusted parts of the workflow.
- The CLI runs with the invoking account's permissions. The browser frame's isolation does not sandbox the CLI or the agent's other tools.
- Round-supplied JavaScript runs without a separate opt-in or confirmation. It is isolated from the feedback shell, but it must not be treated as network-isolated code.

## Shell and content boundary

Lavagna's shell owns decision controls, the feedback editor, send, the conversation capability and the per-round token. Agent-supplied explanations and prototypes render in an opaque-origin `<iframe sandbox="allow-scripts">`; frame responses repeat the sandbox in Content Security Policy (CSP).

The frame has its own per-round resource key, not the shell capability or feedback token. The shell accepts frame messages only from that frame's window; anchor selection must name an anchor defined by the round. The feedback endpoint requires POST, the shell's Origin and the round token. Round scripts cannot read the shell DOM or its editor, obtain its token through that DOM, or submit valid feedback on the user's behalf.

For offline reloads, the shell caches the rendered frame and its named resources and reconstructs an in-memory blob document. It retains `sandbox="allow-scripts"`, embeds resource bytes as data URLs, and pins the copied scripts with CSP hashes and integrity attributes. The shell permits these local snapshot transports; the frame still cannot read the shell or make HTTP, fetch or WebSocket connections. This does not add a script opt-in or change the accepted WebRTC behavior.

The sandbox and CSP restrict storage, popups, form submission, navigation and resource loading. Round resources and `data:` images are allowed; external HTTP resource loads and fetch, beacon and WebSocket connections are blocked by the policy. This is not a guarantee that all network traffic is blocked: WebRTC is an accepted exception below. The raw HTML resource check is a parser lint, not the isolation boundary; browser enforcement is the boundary.

The CLI reads round files under bounded path rules. Code excerpts are read once from regular files under the working directory's Git root and included in the rendered frame. A round script cannot request arbitrary host files through this mechanism, but it can read content already included in its own frame.

## Accepted residual risk: WebRTC

Chrome permits WebRTC traffic despite the frame's CSP, including `connect-src 'none'`. A round script can initiate UDP/STUN traffic to a reachable destination. JavaScript-level blocking is not a reliable boundary; the implementation investigation bypassed it through alternate document and frame contexts.

Consequences include exposing the user's network address to a destination and a possible channel for transmitting information accessible to the script. That information includes the round's rendered text, data and code excerpts. The frame's lack of a shell token does **not** mean it contains no sensitive material.

A review probe reproduced a STUN request from a round script using the actual sandboxed frame, with the destination confined to loopback. It demonstrated network egress; it did not separately demonstrate exfiltration of repository code.

The maintainer explicitly accepted this risk on 2026-10-05 and chose to keep the current behavior. Lavagna adds no script opt-in, per-prototype consent prompt or WebRTC mitigation. This accepts the limit rather than claiming to eliminate it. The decision narrows the earlier blanket statement that content cannot make network requests in the [isolation decision](https://github.com/taekwondodev/dev/issues/95).

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
