package thunderstore

import (
	"bytes"
	"encoding/json"
)

// Manifest is the part of a package's manifest.json that installers read.
type Manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version_number"`
	Namespace    string   `json:"namespace"`
	Author       string   `json:"author"`
	Dependencies []string `json:"dependencies"`
}

// ParseManifest reads a manifest.json. Thunderstore accepts manifests saved with a UTF-8 BOM, and many packages ship
// one, but encoding/json refuses it.
func ParseManifest(b []byte) (Manifest, error) {
	var m Manifest
	err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &m)
	return m, err
}
