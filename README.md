# lavagna

A harness-neutral CLI that brings an agent conversation into the browser. Present questions, collect one explicit feedback batch, and return a machine-readable result to the invoking shell. Questions and discussions stay available across calls.

## Install

```sh
make install
```

Building requires the Go version in [`go.mod`](go.mod). On macOS it also builds `~/Applications/Lavagna.app` for [notifications](docs/rounds.md#get-notified-on-macos), which requires `swiftc` from Xcode or its Command Line Tools (`xcode-select --install`). The binary embeds its browser UI and [Atkinson Hyperlegible Next fonts](internal/page/assets/fonts), licensed under SIL OFL 1.1. See [Development](docs/DEVELOPMENT.md#setup) for platform assumptions.

## Start a round

Run from the agent's invoking shell:

```sh
lavagna check
lavagna round --help
lavagna round < round.md
lavagna close
```

`round` waits for feedback. Read the result and any retained feedback or images before `close` deletes the phase's data. Follow [Browser rounds](docs/rounds.md) for conversation setup, continued calls and recovery, and [Authoring questions](docs/authoring.md) to write `round.md`. Read [Security](SECURITY.md) before presenting sensitive material or scripted prototypes.

## Documentation

| Need | Start here |
| --- | --- |
| Run a phase, read feedback or recover a draft | [Browser rounds](docs/rounds.md) |
| Write questions, excerpts, diagrams or prototypes | [Authoring questions](docs/authoring.md) |
| Understand domain terms | [Domain context](CONTEXT.md) |
| Understand components and architectural decisions | [Architecture](docs/ARCHITECTURE.md) |
| Develop, verify or maintain documentation | [Development](docs/DEVELOPMENT.md) |
| Work on the repository as an agent | [AGENTS.md](AGENTS.md) |
| Use lavagna with the grilling skill | [Grilling skill](https://github.com/taekwondodev/skills/tree/main/skills/grilling) |
