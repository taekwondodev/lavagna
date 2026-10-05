# Project entry point

## Purpose

Lavagna is a harness-neutral CLI for browser rounds in agent conversations. It presents content, awaits one explicit feedback batch and returns a machine-readable result to the invoking shell.

This file guides changes to lavagna itself. Daily use starts in [README.md](README.md) and [the round guide](docs/rounds.md).

## Development workflow

Use `dev-cycle` from the shared workflow library for development work. Keep shared procedures in that library rather than copying them into this repository.

## Conditional references

- Read [CONTEXT.md](CONTEXT.md) before changing domain behavior or terminology.
- Read [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) before changing component responsibilities, conversation ownership, persistence or lifecycle. Follow the relevant decision links before changing those boundaries.
- Read [SECURITY.md](SECURITY.md) before changing resource loading, browser isolation, feedback authorization or access to host files and session transcripts.
- Read [docs/rounds.md](docs/rounds.md) before changing commands, round input, feedback, delivery status or recovery. Update the owning guide in the same change as its behavior.
- Read [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for setup, verification, documentation ownership and recording architectural decisions.
- Read [docs/agents/delivery.md](docs/agents/delivery.md) before implementation or delivery.

## Evidence

Distinguish intended requirements, proposed designs and verified behavior in plans and reports. Support verified claims with an observed result, test or retrieved source; keep change-specific verification with its issue or PR.
