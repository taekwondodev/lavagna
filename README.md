# lavagna

A harness-neutral CLI for browser rounds in agent conversations: present one round of content, await one explicit feedback batch, and return it to the invoking shell as a compact machine-readable result.

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

Run these from the agent's invoking shell. `round` opens the browser and waits for explicit feedback; its last output line is the JSON result. Pass no timeout; Esc interrupts. Use `lavagna round --help` for the grammar and an example.

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
