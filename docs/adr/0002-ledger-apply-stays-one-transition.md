# Ledger.Apply stays one ordered transition

[`Ledger.Apply`](../../internal/conversation/ledger.go) applies a call's elements in one function, one block per element, in the order fixed by the [ledger resolution](https://github.com/taekwondodev/lavagna/issues/18#issuecomment-6034145631): phase, settled, replacements, reply, new questions with the round advance. Read top to bottom, the function is that order, and every block works on the same clone and error list, so an invalid element leaves the whole call unapplied without any helper having to preserve that.

Rejected: one private helper per element, suggested by the Standards review of [#27](https://github.com/taekwondodev/lavagna/issues/27) as a Divergent Change smell. It spreads the order across functions and threads the clone and the errors through each of them, so the order and the all-or-nothing rule would have to be checked in two places.

Read this before splitting `Apply`, adding a call element or changing the order of application. The maintainer chose to keep the single transition on 2026-10-07, during the delivery of #27.
