package thunderstore

import (
	"bytes"
	"encoding/json"
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
	f.Add([]byte("\xef\xbb\xbf" + `{"name":"Mod","version_number":"1.2.3","namespace":"Ns","author":"Au","dependencies":["BepInEx-BepInExPack-5.4.2100"]}`))
	f.Add([]byte("\xef\xbb\xbf" + `{"name":"Mod","version_number":"1.2.3","dependencies":["Ns-Lib-1.0.0"]}`))
	f.Add([]byte(`{"dependencies":"x"}`))
	f.Add([]byte(`{"dependencies":"Ns-Lib-1.0.0"}`))
	f.Add([]byte(``))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := ParseManifest(b)
		if err != nil {
			return
		}
		// encoding/json replaces each invalid UTF-8 byte with U+FFFD (3 bytes).
		bound := 3 * len(b)
		if len(m.Name) > bound || len(m.Version) > bound || len(m.Namespace) > bound || len(m.Author) > bound {
			t.Fatalf("unbounded field from %q: %+v", b, m)
		}
		for _, d := range m.Dependencies {
			if len(d) > bound {
				t.Fatalf("unbounded dependency %q from %q", d, b)
			}
		}
		enc, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		rt, err := ParseManifest(enc)
		if err != nil || rt.Name != m.Name || rt.Version != m.Version || rt.Namespace != m.Namespace || rt.Author != m.Author || !slices.Equal(rt.Dependencies, m.Dependencies) {
			t.Fatalf("round-trip %+v from %q: %+v %v", m, b, rt, err)
		}
		if bytes.HasPrefix(b, []byte("\xef\xbb\xbf")) {
			return
		}
		again, err := ParseManifest(append([]byte("\xef\xbb\xbf"), b...))
		if err != nil || again.Name != m.Name || !slices.Equal(again.Dependencies, m.Dependencies) {
			t.Fatalf("BOM changed the parse of %q: %+v vs %+v (%v)", b, m, again, err)
		}
	})
}
