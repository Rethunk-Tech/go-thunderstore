# Changelog

Notable changes to `go-thunderstore`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.3.2] - 2026-11-02

### Fixed

- `Uploader` refuses a redirect from https to http, as `Client` does, so a downgrade never carries the upload token.

## [0.3.1] - 2026-11-02

### Fixed

- `Upload` refuses an upload id that is not a UUID and parts that do not tile the file from offset 0 (at most 10000), and error bodies in messages are cut to 300 bytes.

## [0.3.0] - 2026-11-02

### Added

- `Client.Categories` lists a community's package categories (`Category{Slug, Name}`).
- `ParseDependency` and `Dependency` (`String`) split and format a manifest dependency, `Namespace-Name-1.2.3`.

### Changed

- A non-2xx answer to an `Uploader` call is a `*httpkit.StatusError` (`errors.As`), and a 404 matches `httpkit.ErrNotFound`, as the `Client`'s already was; the message text is unchanged.

## [0.2.0] - 2026-11-02

### Added

- `Uploader` (`NewUploader`, `Upload`, `Submit`) uploads a package zip in the parts Thunderstore's storage asks for and submits it, as Thunderstore's CLI does.

## [0.1.2] - 2026-10-30

### Changed

- `Package.Repo` validates the GitHub owner/repo with go-github's `ValidRepo` instead of an inlined copy.

## [0.1.1] - 2026-10-30

### Added

- `Client.Stored` reads a community's listing without the network: the held index, else the stored file however old,
  and `false` when nothing is stored. It never downloads, builds or starts a refresh.
- `Client.StoredVersions` answers a package's versions under the same never-network rule.
- `Client.Held` reports whether `Index` keeps the listing in memory.

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

[Unreleased]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.3.2...HEAD
[0.3.2]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Rethunk-Tech/go-thunderstore/releases/tag/v0.1.0
