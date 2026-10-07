# lavagna

A harness-neutral CLI for browser rounds in agent conversations: present one or more questions, await one explicit feedback batch, and return question-grouped machine-readable results to the invoking shell.

## Install

```sh
make install
```

The binary uses only the Go standard library and embeds the page, the v3 stylesheet and Atkinson Hyperlegible Next (SIL OFL 1.1, see [`internal/page/assets/fonts/`](internal/page/assets/fonts)).

## Use

```sh
lavagna check
lavagna round < round.md
lavagna close
```

Run these from the agent's invoking shell. `round` presents one or more per-question documents and waits for explicit feedback; stdout is one JSON result and operational status goes to stderr. Pass no timeout; Esc interrupts. Start with `lavagna round --help` for the minimal contract and example; load `lavagna round --help grammar` for the full format. Read deferred feedback with `lavagna feedback SUBMISSION --question ID`, `--overview` or `--all`. Read needed feedback and images before `close`, which deletes the phase's retained data.

The [round guide](docs/rounds.md) covers conversation identity, browser setup, commands, directory input, feedback limits, delivery status and recovery. Read [Security](SECURITY.md) before presenting sensitive material or scripted prototypes.

## Project documentation

| Need | Start here |
| --- | --- |
| Use the CLI and recover a draft | [Browser rounds](docs/rounds.md) |
| Understand the vocabulary | [Domain context](CONTEXT.md) |
| Find component responsibilities and design decisions | [Architecture](docs/ARCHITECTURE.md) |
| Set up development, run checks or maintain documentation | [Development](docs/DEVELOPMENT.md) |
| Change the project as an agent | [Agent entry point](AGENTS.md) |
| Prepare a pull request | [Delivery policy](docs/agents/delivery.md) |
| Understand trust boundaries and accepted risks | [Security](SECURITY.md) |
| Use lavagna with the grilling skill | [Grilling skill](https://github.com/taekwondodev/skills/tree/main/skills/grilling) |
