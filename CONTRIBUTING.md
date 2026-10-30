# Contributing

Read [`AGENTS.md`](AGENTS.md) first.

## Commits

`type(scope): subject` — imperative, about 72 characters, one logical unit, why in the body, no AI
trailers.

## Gate

```bash
gate
```

`gate` is what the CI gate job runs.

## Exports

The module has one consumer. A new export needs a call site there, not a use for it here.
Tests are in the same package and exercise unexported code directly, so they are not a reason to
export anything.

## Dependencies

A new dependency needs a reason the standard library cannot meet.

## Changelog

[`CHANGELOG.md`](CHANGELOG.md): Keep a Changelog, newest first. Add to Unreleased in the same commit
as a behaviour change. Refactors, tests and docs do not.

Tag `vX.Y.Z` to cut a release.
