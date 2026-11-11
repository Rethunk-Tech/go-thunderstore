# Security

## Supported versions

The latest tagged release. There are no long-lived release branches, so a fix ships in the next tag
rather than being backported.

## Reporting a vulnerability

Use GitHub's private vulnerability reporting on this repository: <https://github.com/Rethunk-Tech/go-thunderstore/security/advisories/new>, or the **Security** tab, then **Report a vulnerability**. The report stays private between you and us until a fix ships.

If private reporting is unavailable, email [oss@rethunk.tech](mailto:oss@rethunk.tech) with **[SECURITY]** in the subject.

Please do not open a public issue, pull request or discussion for a suspected vulnerability.

A useful report has the affected repository and version (or commit), steps to reproduce, what an attacker gets and under which conditions, and whether anyone else knows.

## What counts as a vulnerability

A vulnerability is a flaw that lets someone cross a boundary they should not: run code, read or change data they have no right to, escape a sandbox or path restriction, or leak a credential. A bug is anything else that misbehaves, such as a crash on your own input, wrong output or a slow path; open an ordinary issue for those. If you are unsure which one you have, report it privately and we will say.

## Coordinated disclosure

We handle the report inside the private advisory, so you see each status change and can comment on the fix. The advisory goes public when the fix ships, or 90 days after your report, whichever comes first. If a fix is close, either of us can ask for more time in the thread. You are credited in the advisory unless you would rather not be.

## Response timelines

- We acknowledge your report within 3 business days.
- We give you an assessment (accepted, needs more information or declined, with reasons) within 10 business days.
- We aim to ship a fix within 90 days of the report.

## What is in scope

- Index and chunk responses (`Client.Index`, `Each`, `Find`, `Versions`): remote JSON, possibly gzip. Each body is read
  capped (16 MiB) and each gzip stream capped on unpacking (64 MiB), failing as `safefs.ErrTooLarge` rather than
  allocating without bound or parsing a truncation. At most 500 chunks are followed. A chunk must not panic the decoder or the slimming (`FuzzIndexChunkDecode`).
- Chunk addresses named by the index: only the configured base host or https `thunderstore.io` hosts are fetched; a
  crafted index must not make the client request another host (an SSRF), and one bad address aborts the build. A
  redirect from any request must also land on one of those hosts.
- Community keys: a key that is not lowercase letters, digits and hyphens is refused before a request or a path is
  built, so it cannot move the request URL or the cache path.
- Stored listings: a cache file with a dependency index past its name table is refused, not indexed (no panic).
- `ParseLink`: a link string from the operating system or a web page. Only the exact `ror2mm` grammar passes; namespace
  and name cannot hold a separator, dots or `..`, and a query, fragment or userinfo is refused (`FuzzParseLink`).
- `ParseManifest`: a package's `manifest.json` is untrusted JSON and must not panic the parser (`FuzzParseManifest`).
- Upstream strings `Package.Repo` and `Package.Donation`: `Repo` is a validated `owner/repo`, `Donation` is kept only
  as an http or https address, so neither carries a `javascript:` or `file:` link.
- Upload parts: sent over https to one host the site names, never an IP address; `Uploader.AllowStorageHosts` pins the host,
  since each Thunderstore deployment sets its own storage endpoint and no host can be assumed.

## What is out of scope

- The caller's `HTTP` client, `BaseURL` and `CacheDir`: a hostile transport or a shared, writable cache folder is the
  caller's to prevent.
- What the caller does with a package's text, icon address or dependency strings; names and summaries are the
  publishers' words and are not sanitised for display.
- Verifying downloaded package files: this module lists packages and parses links, and downloads none.
- A site that serves a wrong but well-formed index.

## Safe harbour

We will not pursue legal action against good-faith research that avoids privacy violations, data destruction and disruption of services, stops at a proof of concept, and keeps the details private until the advisory is published. Rethunk.Tech, LLC maintains this repository.
