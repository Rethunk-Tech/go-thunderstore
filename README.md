<h1 align="center">go-thunderstore</h1>

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.27.3+-00ADD8?logo=go&logoColor=white)](go.mod)

</div>

---

Reads Thunderstore's package index into a compact on-disk listing, with CDN ordering, ror2mm links and manifest.json. Package `thunderstore`, module `github.com/Rethunk-Tech/go-thunderstore`.

For installers and mod managers that browse Thunderstore. A community's index is downloaded once, stored as a compact
listing, and answered from disk after that: a stale listing is served at once while a rebuild runs behind it. Oversized
or hostile answers fail with an error instead of exhausting memory.

## Quick start

```bash
go get github.com/Rethunk-Tech/go-thunderstore
```

A worked example and the contracts: [HUMANS.md](HUMANS.md).

## Features

- `Client` keeps a community's package index on disk, an hour fresh, and refreshes it in the background.
- `Index`, `Client.Each` and `Client.Find` read it whole or a package at a time; repeated dependency names and lists are held once.
- `Index` holds a memoised listing compactly: dependency lists as varints, shared category lists, about a quarter less memory.
- `Client.Versions` answers a package's version list from a small side file, without loading the listing.
- `Uploader` uploads a package zip and submits it under a team, as Thunderstore's CLI does.
- Every answer is size-capped and every index-named chunk address is checked.
- `ParseLink`, `CDNOrder`, `OnHost` and `ParseManifest` cover `ror2mm://` links, download hosts and `manifest.json`.

## Documentation

| Doc | Audience |
| --- | --- |
| [HUMANS.md](HUMANS.md) | Running and using the package |
| [AGENTS.md](AGENTS.md) | Internals, invariants, and why |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Process — commits, tests, docs |
| [SECURITY.md](SECURITY.md) | Vulnerability disclosure |
| [CHANGELOG.md](CHANGELOG.md) | Release notes |

## License

MIT — see [LICENSE](LICENSE). © 2026 Rethunk.Tech, LLC.
