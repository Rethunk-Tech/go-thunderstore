# HUMANS.md

Run and use `go-thunderstore`. Internals: [AGENTS.md](AGENTS.md).

## Quick start

```bash
go get github.com/Rethunk-Tech/go-thunderstore
```

```go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Rethunk-Tech/go-thunderstore"
)

func main() {
	base, _ := os.UserCacheDir()
	c, err := thunderstore.New(thunderstore.Config{
		CacheDir:  filepath.Join(base, "myapp"),
		UserAgent: "myapp/1.0 (+https://example.com)",
		Refreshed: func(community string) { fmt.Println("new listing for", community) },
	})
	if err != nil {
		fmt.Println(err)
		return
	}
	defer c.Wait()

	ctx := context.Background()
	p, ok, err := c.Find(ctx, "lethal-company", "BepInEx", "BepInExPack")
	if err != nil || !ok {
		fmt.Println("not found", err)
		return
	}
	fmt.Println(p.Owner, p.Name, p.Versions[0].Number)

	idx, err := c.Index(ctx, "lethal-company")
	if err != nil {
		fmt.Println(err)
		return
	}
	if q, ok := idx.Find("BepInEx", "BepInExPack"); ok {
		fmt.Println(q.Dependencies(q.Versions[0]))
	}
}
```

## Contracts

| Behaviour | Contract |
| --- | --- |
| `New` | Fails without `Config.CacheDir`; files live in its `thunderstore` folder; use one `Client` per folder |
| `Client.Index`, `Each`, `Find`, `Versions` | A stored listing answers at once however old; past an hour a rebuild runs behind it; only a community with nothing stored waits on the download |
| `Client.Index` | Holds the listing in memory until unused for ten minutes; one package per case-folded id |
| `Client.Each`, `Find`, `EachStored` | Read a package at a time; `Package.Dependencies` is empty; `Each` and `EachStored` may give a case-folded twin twice, `Package.Outranks` picks |
| `Client.Snapshot` | The held index if there is one, else a fresh read the client does not keep |
| `Config.Refreshed` | Called with the community after a background refresh stored a changed listing, never for an unchanged one |
| `Config.Built` | Called with the community and listing path after each new listing is stored |
| `Client.Refresh` | Marks the listing stale and rebuilds in the background; readers keep the old one until it lands |
| `Client.Wait` | Blocks until started background refreshes end |
| Download failure | A stored listing is kept and served; with none stored the error is returned |
| Size caps | A chunk over 16 MiB, or one that unpacks past 64 MiB, fails as `safefs.ErrTooLarge` |
| Community key | Lowercase letters, digits and hyphens only; anything else is refused before any request |
| Chunk addresses | Only the base URL's host or https `thunderstore.io` hosts; one other address fails the whole build |
| `ParseLink` | Accepts only `ror2mm://v1/install/thunderstore.io/<namespace>/<name>/<x.y.z>/` |
| `CDNOrder`, `OnHost` | `CDNOrder` is nil for an address off the given hosts; `OnHost` keeps path and query |
| `ParseManifest` | Accepts a leading UTF-8 BOM |
| `Package.VersionNumbers` | Newest first by numeric `x.y.z`; an unparseable number sorts as equal |

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
