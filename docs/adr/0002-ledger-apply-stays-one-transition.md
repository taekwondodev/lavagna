# Ledger.Apply stays one ordered transition

Keep [`Ledger.Apply`](../../internal/conversation/ledger.go) as one ordered transition, with one block per call element sharing the same clone and error list. Reading the function top to bottom exposes the application order and the all-or-nothing rule together. The [round guide](../rounds.md#continue-a-phase) owns the call semantics.

Rejected: a private helper per element. That would distribute the order across functions and thread the clone and errors through them, making the same invariant harder to inspect.

Read this before splitting `Apply`, adding a call element or changing application order. The maintainer chose the single transition on 2026-10-07 during delivery of [#27](https://github.com/taekwondodev/lavagna/issues/27); [PR #36](https://github.com/taekwondodev/lavagna/pull/36) records the review and acceptance. The [ledger resolution](https://github.com/taekwondodev/lavagna/issues/18#issuecomment-6034145631) supplies the original ordering constraint.
