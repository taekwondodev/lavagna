# Issue tracker: GitHub

Issues, specs, and tickets for this repo live as GitHub issues. Before GitHub operations, load the `github-cli` skill for command selection and readback.

## Conventions

- **Create an issue**: `gh-axi issue create --title "..." --body "..."`. Use `--body-file <path>` for multi-line bodies.
- **Read an issue**: `gh-axi issue view <number> --comments --full`; inspect labels and relevant comments as well as the body.
- **List issues**: `gh-axi issue list --state open` with appropriate `--label`, `--state`, and `--limit` filters. Fetch relevant issues individually for their full bodies and comments.
- **Comment on an issue**: `gh-axi issue comment <number> --body "..."`
- **Update an issue body**: `gh-axi issue edit <number> --body-file <path>`; preserve the existing issue number and comments.
- **Apply / remove labels**: `gh-axi issue edit <number> --add-label "..."` / `--remove-label "..."`
- **Close**: `gh-axi issue close <number> --comment "..."`.

Infer the repo from `git remote -v`; `gh-axi` does this automatically when run inside a clone.

## Media

GitHub's API cannot attach files to issues, so publish issue media this way:

- **Text artifacts** (round sources, prototypes, recaps, measurement data): post the complete text as issue comments, one fenced block per file with its path, split across comments when a body would exceed GitHub's 65,536-character limit.
- **Binary media** (screenshots, images): commit them to an orphan branch `media/<topic>` on `origin`, push it, and embed each file in the issue with a link pinned to that commit (`https://github.com/<owner>/<repo>/blob/<sha>/<path>?raw=true`). Do not delete a media branch that an open or closed issue still references.

Never leave the only copy of a published artifact in a local temporary directory.

## When a skill says "publish to the issue tracker"

Create a GitHub issue.

## When a skill says "fetch the relevant ticket"

Run `gh-axi issue view <number> --comments --full`.

## Quick issue capture

Used by `/capture-issue`. Read `docs/agents/triage-labels.md` for the category and initial state labels, then create the issue with those labels.

When `/to-spec` completes an existing issue, update its body in place with `gh-axi issue edit <number> --body-file <path>` and apply the spec-publication transition from `docs/agents/triage-labels.md`. Read back the body and labels. Do not create a replacement issue.

## Wayfinding operations

Used by `/wayfinder`. Read its "Ticket Types" section when choosing child-ticket labels.

- **Map**: create one issue with `gh-axi issue create --label wayfinder:map`, using the map body from `/wayfinder`.
- **Child ticket**: an issue linked to the map as a GitHub sub-issue with `gh-axi issue subissue add <map> <child>`. Where sub-issues aren't enabled, add the child to a task list in the map body and put `Part of #<map>` at the top of the child body. Once claimed, the ticket is assigned to the driving dev.
- **Blocking**: GitHub's **native issue dependencies**, the canonical, UI-visible representation. Add an edge with `gh-axi api POST /repos/<owner>/<repo>/issues/<child>/dependencies/blocked_by --field issue_id=<blocker-db-id>`, where `<blocker-db-id>` is the blocker's numeric **database id** (`gh-axi api /repos/<owner>/<repo>/issues/<n> --jq .id`, not the `#number` or `node_id`). Read the child's live open-blocker count with `gh-axi api /repos/<owner>/<repo>/issues/<child> --jq .issue_dependencies_summary.blocked_by`. Where dependencies aren't available, fall back to a `Blocked by: #<n>, #<n>` line at the top of the child body. A ticket is unblocked when every blocker is closed.
- **Frontier query**: list the map's children with `gh-axi issue subissue list <map>`, or read its task list when using the fallback. Keep only open children, then drop any with an open blocker (`issue_dependencies_summary.blocked_by > 0`, or an open issue in the `Blocked by` line) or an assignee; first in map order wins.
- **Claim**: `gh-axi issue edit <n> --add-assignee @me`. This is the session's first write. In the same update, apply the work-start activity transition from `docs/agents/triage-labels.md`; read back the assignee and labels.
- **Resolve**: `gh-axi issue comment <n> --body "<answer>"`, then `gh-axi issue close <n>`, then append a context pointer (gist + link) to the map's Decisions-so-far.

## Native development links

Use GraphQL through `gh-axi api POST /graphql --field query='<operation>' --full`. Paginate connections to exhaustion.

- **Branch readback:** query `Issue.linkedBranches`, matching each `ref` by repository and branch name; reuse existing associations.
- **New remote branch:** use `createLinkedBranch(input: ...)` before the first push creates the ref, with explicit `issueId`, `repositoryId`, `name`, and `oid`. IDs are GraphQL node IDs; `oid` is the verified branch-point commit already available in that repository. Read back the association and ref.
- **Existing remote branch:** use a supported linking operation that preserves the ref. Report unavailable operations as pending associations.
- **PR readback:** query `PullRequest.closingIssuesReferences` and compare qualified issue identities. A body mention alone is not a native link.

Linked branches auto-link future PRs.

## Tracer-bullet ticket operations

Used by `/to-tickets`, `/implement`, `/commit`, and `/pr`.

- **Ticket**: a GitHub issue, one per tracer-bullet slice. Body states the affected components or layers from the approved architecture and the behaviour to build.
- **Blocking**: same native issue dependencies as wayfinding above.
- **Grabbing work**: any ticket whose blockers are all closed and which is unassigned is takeable. Use the claim operation above before `/implement` starts.
- **Delivery**: read `docs/agents/delivery.md` for the repository default. Follow `/commit` for local commits and direct-delivery issue updates, or `/pr` for PR publication and issue links.
