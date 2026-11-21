# Changelog

Notable changes to `go-thunderstore`. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.4.2] - 2026-11-21

### Fixed

- Upload: the target is checked on every request and behind a proxy, so a caller TLS dialer or wrapped transport cannot skip it; private hosts, disguised IPv4, non-ASCII and localhost hosts, and storage dials to private resolved IPs are refused.
- Upload: storage and `Submit` page URLs must be http(s), 307 and 308 `PUT` hops off the storage host are refused, and the signed URL is dropped from part upload errors.
- `OnHost` requires absolute http(s) URLs on both sides and drops userinfo when rewriting onto a host.
- Install links refuse percent escapes, query and fragment, and listing names refuse path separators.
- A gzip chunk with a truncated or wrong trailer fails, and an index with no packages is refused instead of replacing the listing.
- `Built` runs outside the build lock and only when a listing is newly stored, old listings are dropped after the meta write, and `Stored` no longer serves a listing a refresh already replaced.
- Unparseable version numbers sort last so the order is total, and `ParseDependency` errors truncate the input they echo.

## [0.4.1] - 2026-11-08

### Changed

- A memoised listing keeps each dependency list once as a run of varints and shares repeated category lists, about a quarter less memory held for `Client.Index`'s idle window. The stored file and every answer are unchanged.

## [0.4.0] - 2026-11-07

### Added

- `Uploader.AllowStorageHosts` pins the hosts an upload may send its zip to.

### Fixed

- `Upload` sends the zip to one named storage host.
- `Upload` refuses part bounds that overflow, and the client refuses path-like dependencies.
- Redirects are held to the chunk hosts, a listing is capped, a build keeps sibling communities' listings, and a listing lost to a concurrent refresh is retried.

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

[Unreleased]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.4.2...HEAD
[0.4.2]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.4.1...v0.4.2
[0.4.1]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.3.2...v0.4.0
[0.3.2]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.3.1...v0.3.2
[0.3.1]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/Rethunk-Tech/go-thunderstore/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/Rethunk-Tech/go-thunderstore/releases/tag/v0.1.0
