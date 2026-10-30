# HUMANS.md

Run and use `go-thunderstore`. Internals: [AGENTS.md](AGENTS.md).

## Quick start

```bash
go get github.com/Rethunk-Tech/go-thunderstore
```

<!-- scaffold: the smallest complete Go example of the main entry point, compiled in your head
against the real API: imports implied, errors handled. -->

## Contracts

<!-- scaffold: a two-column table, one row per exported entry point or behaviour a caller depends
on: what it refuses, what it guarantees on failure, what it never does. -->

## Requirements

Go 1.27.3 or newer, plus `lefthook` and `gitleaks` on PATH if you want the git hooks.

```bash
lefthook install
```

That is per clone; git does not commit hooks. Pre-commit scans staged changes for secrets, and
pre-push runs `gate`.

## Verify

```bash
gate
```
