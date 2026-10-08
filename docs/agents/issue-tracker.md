# Issue tracker

Use GitHub Issues on [`taekwondodev/lavagna`](https://github.com/taekwondodev/lavagna/issues) for specs, implementation tickets and decision questions. Load the shared `github-cli` skill before GitHub operations.

## Work records

Use `gh-axi issue` for reads and mutations. Fetch full context with `gh-axi issue view N --comments --full`, including labels and decision amendments. For `create`, `edit` and `comment`, use `--body-file` for multiline text. Consult `gh-axi issue --help` for supported flags, including label, assignment and close operations.

- Apply [label policy](triage-labels.md) when creating an issue, publishing a complete spec or implementation ticket, or starting, resuming or parking work.
- Use native sub-issues for parent-child relationships and native issue dependencies for blockers. If unavailable, use a parent task list with `Part of #N` in each child, and `Blocked by: #N` for blocking edges.
- Claim selected implementation work with the driving developer as assignee and the work-start activity label. A ticket is available only when unassigned and all blockers are closed.

## Wayfinding operations

Represent the map and its decision tickets as GitHub issues, using [work records](#work-records) for claims and parent-child relationships. List native sub-issues in map order, or read the parent task list when using the fallback; use the dependency readback below to determine blockers.

## Tracer-bullet ticket operations

Represent each approved implementation slice as a GitHub issue. Use [work records](#work-records) for assignment and relationships, and [native development links](#native-development-links) for branch and PR associations.

## Native development links

Use native linked branches and PR closing references, not body mentions as a proxy. Read [delivery policy](delivery.md) before preparing them. Linked branches can propagate closing associations, so follow the shared `pr` skill's issue-link rules for the intended target.

GitHub operations not exposed by a `gh-axi` subcommand use its API command:

| Relationship | API contract |
| --- | --- |
| Issue blocker | REST `issues/<child>/dependencies/blocked_by`; `issue_id` is the blocker's numeric database ID, not its issue number |
| Blocker state | `issue_dependencies_summary.blocked_by` on the child issue |
| Linked branch | GraphQL `Issue.linkedBranches`; match repository and branch name |
| Create linked branch | GraphQL `createLinkedBranch`, before the first push creates the remote ref; use the verified branch-point commit |
| PR closing scope | GraphQL `PullRequest.closingIssuesReferences`; compare repository-qualified issue identities |

Paginate relationship reads. Reuse existing associations and preserve existing refs; report an unavailable linking operation rather than recreating a branch.

## Media

GitHub's API cannot attach files to issues. Publish artifacts as follows:

- **Text:** complete content in issue comments, one fenced block per file with its path. Split comments below GitHub's 65,536-character body limit.
- **Binary:** files on an orphan `media/<topic>` branch on `origin`, embedded with a commit-pinned URL: `https://github.com/<owner>/<repo>/blob/<sha>/<path>?raw=true`. Retain branches referenced by open or closed issues.

A local temporary directory is not a published artifact. Read back the published content or pinned link.
