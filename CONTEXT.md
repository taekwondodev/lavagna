# Domain context

Lavagna connects an agent conversation to a browser page for explicit user feedback. This glossary owns vocabulary; [Architecture](docs/ARCHITECTURE.md) owns component responsibilities and [Rounds](docs/rounds.md) owns observable behavior.

| Term | Meaning |
| --- | --- |
| **Harness** | The environment invoking lavagna on behalf of an agent conversation. |
| **Conversation** | The identity binding successive CLI calls to one page origin and retained state. |
| **Round** | One presentation of authored content and decisions, awaiting one explicit feedback batch. |
| **Content snapshot** | Retained rendered explanations and resource bytes reusable by another round without inheriting its decisions. |
| **Shell** | The trusted browser UI that owns decisions, the feedback editor and send. Not the invoking terminal shell. |
| **Content frame** | The isolated browser frame displaying the round's explanations and prototypes. |
| **Anchor** | An authored name identifying content to which the user can attach a comment. |
| **Draft** | Editable choices, comments and screenshot attachments not yet conclusively returned by a round call. |
| **Feedback batch** | The choices, comments and screenshots submitted by one explicit send action. |
| **Deferred feedback** | A batch whose full retained record is accessed selectively instead of being returned entirely in the round's stdout. |
| **Submission ID** | The identity of a batch, retained across retries to recognize duplicates. |
| **Page origin** | The conversation's loopback address and port, accessed through its capability-bearing path. |
| **Capability** | The secret in the page path that identifies access to the conversation's HTTP endpoints. |
| **Round token** | The per-round secret required to upload screenshots or send feedback. |
| **Frame key** | The separate per-round resource key used by the content frame, without the shell's capability or round token. |
| **Lease** | The exclusive file lock serializing CLI calls for one conversation. |
| **Relay** | The process holding the page origin between calls during the agent's turn. |
| **Witness** | The read-only observer of a Pi session that supplies evidence of feedback receipt and turn completion. |
| **Delivery stage** | A receipt supported by an observed event, such as server acceptance, CLI return or harness receipt; not an inference that the agent understood the feedback. |
| **Sweep** | Removal of another conversation's expired local state while holding its lease. |
