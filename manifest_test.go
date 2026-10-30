package thunderstore

import (
	"bytes"
	"slices"
	"testing"
)

func TestParseManifestAcceptsABOM(t *testing.T) {
	m, err := ParseManifest([]byte("\xef\xbb\xbf" + `{"name":"Mod","version_number":"1.2.3","namespace":"Ns","author":"Au","dependencies":["BepInEx-BepInExPack-5.4.2100"]}`))
	if err != nil || m.Name != "Mod" || m.Version != "1.2.3" || m.Namespace != "Ns" || m.Author != "Au" ||
		!slices.Equal(m.Dependencies, []string{"BepInEx-BepInExPack-5.4.2100"}) {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ParseManifest([]byte(`{"dependencies":"x"}`)); err == nil {
		t.Fatal("a string for the dependency list was accepted")
	}
}

// A parse that succeeds on a BOM-prefixed input succeeds on the bare one with the same result.
func FuzzParseManifest(f *testing.F) {
	f.Add([]byte("\xef\xbb\xbf" + `{"name":"Mod","version_number":"1.2.3","dependencies":["Ns-Lib-1.0.0"]}`))
	f.Add([]byte(`{"dependencies":"Ns-Lib-1.0.0"}`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := ParseManifest(b)
		if err != nil || bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
			return
		}
		again, err := ParseManifest(append([]byte("\xef\xbb\xbf"), b...))
		if err != nil || again.Name != m.Name || !slices.Equal(again.Dependencies, m.Dependencies) {
			t.Fatalf("BOM changed the parse of %q: %+v vs %+v (%v)", b, m, again, err)
		}
	})
}
