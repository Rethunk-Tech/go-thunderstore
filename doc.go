// Package thunderstore reads Thunderstore's package index for installers and mod managers. A Client downloads a
// community's index chunk by chunk into a compact on-disk listing (dependency names held once), answers from it at
// once however old, and refreshes it in the background. The package also parses ror2mm install links and
// manifest.json, and orders Thunderstore's download hosts.
package thunderstore
