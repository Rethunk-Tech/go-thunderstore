# AGENTS.md

Package `thunderstore` reads Thunderstore's package index into an on-disk compact listing, serves it through `Client`,
and parses `ror2mm://` links and `manifest.json`. Installers and mod managers import it. It imports no consumer
package, only go-github, go-httpkit, go-safefs and go-semverish; search ranking, installing and wording a player reads stay in the caller.

## Gate

`gate` is the command. Each override in `.gate.toml` is there because a runner
cannot resolve that check from PATH.

## Shape

- `Client` is the only holder of state (memo, build locks, refresh bookkeeping); nothing is process-wide, so tests
  and two caches never share it. Two `Client`s on one cache folder do not coordinate builds.
- `buildListing` is the single path that downloads. It runs once per community at a time, keeps a stored listing when
  the site is unreachable, and checks every chunk address before fetching any (`chunkAllowed`): the index is remote
  data and must not aim the client at another host.
- A listing is built by streaming each chunk into `safefs.WriteStreamAtomic`, never held whole. The file is two JSON
  values, the packages then the dependency-name table they index; `readPackages` refuses an index past the table.
- `listingTag` changes with the stored shape, so a listing of another shape is never read. File names and the JSON
  tags are what an existing cache holds: change a tag only with the tag.
- Size bounds are policy against hostile answers: `maxBody` per HTTP body (`safefs.CapReader`), `maxUnpacked` per gzip
  stream. An oversize answer is an error, never a truncated parse.
- `Versions` is answered from the side file only when its stamped hash equals the stored index's, so it never serves
  versions from a different index. A refresh rewrites it for the packages it already held.
- Background refreshes use `context.WithoutCancel` plus `refreshTimeout`: the caller's request ending must not abort a
  rebuild that other readers wait on. `refreshAfter` is tried once per window, so an offline start does not retry.
- `Package.Outranks` is the one rule for which of two case-folded twins wins; `dedupeIDs`, `Client.Find` and the side
  file all use it.
- `ParseLink` accepts a closed grammar; any widening needs the fuzz properties in `link_test.go` updated first.

## Files

| File | Holds |
| --- | --- |
| `doc.go` | Package comment |
| `client.go` | `Config`, `Client`, `New`, the fetch and build path, the listing files |
| `index.go` | `Index`, the dependency-name table and compactor, the memo, `EachStored` |
| `package.go` | `Package`, `Version`, the wire shape and its slimming |
| `side.go` | `Versions`, the side file, background refresh, `Refresh` |
| `link.go` | `Ref`, `ParseLink` |
| `cdn.go` | `CDNs`, `OnHost`, `CDNOrder` |
| `manifest.go` | `Manifest`, `ParseManifest` |
