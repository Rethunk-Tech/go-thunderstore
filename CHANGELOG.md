# Changelog

Notable changes to `go-thunderstore`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] - 2026-10-30

### Added

- `Client` reads a community's package index through an on-disk cache: stale listings are served at once while a
  rebuild runs in the background, `Config.Refreshed` reports a changed listing, oversized or hostile answers are
  refused.
- `Index`, `Client.Each`, `Client.Find` and `EachStored` read the compact listing whole or a package at a time.
- `Client.Versions` answers a package's versions from a side file without loading the listing.
- `ParseLink` reads `ror2mm://` install links.
- `CDNs`, `OnHost` and `CDNOrder` order Thunderstore's download hosts.
- `ParseManifest` reads a package's `manifest.json`, BOM included.

[Unreleased]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Rethunk-Tech/go-thunderstore/releases/tag/v0.1.0
