package thunderstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"testing"
	"time"
)

func TestDepCompactorSharesIdenticalListsAndKeepsTheDependencies(t *testing.T) {
	t.Parallel()
	deps := []string{"BepInEx-BepInExPack-5.4.2100", "Owner-Lib-1.0.0"}
	names := depNames{index: map[string]uint32{}}
	mk := func(owner string) Package {
		return Package{Owner: owner, Versions: []Version{{Number: "1.0.0", deps: names.ids(deps)}, {Number: "1.0.1", deps: names.ids(deps)}}}
	}
	c := newDepCompactor()
	a, b := mk("A"), mk("B")
	c.add(&a)
	c.add(&b)
	c.tab.names = names.names
	if len(names.names) != len(deps) {
		t.Errorf("names = %v, want each dependency once", names.names)
	}
	if &a.Versions[0].deps[0] != &b.Versions[1].deps[0] {
		t.Error("identical dependency lists must share one id slice")
	}
	if got := a.Dependencies(a.Versions[1]); !slices.Equal(got, deps) || !c.known() {
		t.Errorf("deps = %v, want %v", got, deps)
	}
}

func writeListing(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "listing.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAListingWhoseVersionNamesADependencyItDoesNotHoldIsRefused(t *testing.T) {
	path := writeListing(t, `[{"owner":"A","name":"M","versions":[{"v":"1.0.0","d":[0,1]}]}]`+"\n"+`["Only-One-1.0.0"]`)
	if _, err := readPackages(path); err == nil {
		t.Fatal("an index past the names must fail the load, not panic at the first lookup")
	}
}

func TestALoadedListingIsReleasedOnceIdleAndReadAgainOnTheNextUse(t *testing.T) {
	c, err := New(Config{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	c.listingIdle = 20 * time.Millisecond
	path := writeListing(t, `[{"owner":"A","name":"M","versions":[{"v":"1.0.0","d":[0]}]}]`+"\n"+`["Only-One-1.0.0"]`)
	held := func() bool {
		_, ok := c.memoized("k", path)
		return ok
	}
	if _, err := c.load("k", path); err != nil || !held() {
		t.Fatalf("load: %v, held = %v", err, held())
	}
	var st debug.GCStats
	debug.ReadGCStats(&st)
	collections := st.NumGC
	waitFor(t, func() bool { return !held() })
	waitFor(t, func() bool { debug.ReadGCStats(&st); return st.NumGC > collections })
	x, err := c.load("k", path)
	if err != nil || len(x.Packages()) != 1 || !slices.Equal(x.Packages()[0].Dependencies(x.Packages()[0].Versions[0]), []string{"Only-One-1.0.0"}) {
		t.Fatalf("reload = %+v, %v", x, err)
	}
}

// A package the chunked listing caught twice, or a deprecated one with a live twin, is kept once.
func TestDedupeKeepsTheLiveThenTheNewerListing(t *testing.T) {
	t.Parallel()
	v := []Version{{Number: "1.0.0"}}
	got := dedupeIDs([]Package{
		{Owner: "akechii", Name: "DiscountAlert", Updated: "2025-01-01", Versions: v},
		{Owner: "Other", Name: "Mod", Updated: "2025-01-01", Versions: v},
		{Owner: "Akechii", Name: "discountalert", Updated: "2025-02-01", Versions: v},
		{Owner: "AKECHII", Name: "DISCOUNTALERT", Updated: "2030-01-01", Deprecated: true, Versions: v},
	})
	if len(got) != 2 || got[1].Updated != "2025-02-01" || got[0].Name != "Mod" {
		t.Fatalf("dedupe = %+v", got)
	}
}

func TestVersionNumbersSortNewestFirst(t *testing.T) {
	t.Parallel()
	p := Package{Versions: []Version{{Number: "1.9.0"}, {Number: "1.10.0"}, {Number: "2.0.0"}}}
	got := p.VersionNumbers()
	if got[0] != "2.0.0" || got[1] != "1.10.0" || got[2] != "1.9.0" {
		t.Fatalf("order = %v", got)
	}
}

func TestGitHubRepoReadsOnlyRepositoryAddresses(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		"https://github.com/Ns/Repo":         "Ns/Repo",
		"https://www.github.com/Ns/Repo.git": "Ns/Repo",
		"https://github.com/Ns/Repo/tree/x":  "Ns/Repo",
		"https://github.com/Ns":              "",
		"https://github.com/../Repo":         "",
		"https://gitlab.com/Ns/Repo":         "",
		"not a url":                          "",
	} {
		if got := githubRepo(raw); got != want {
			t.Errorf("githubRepo(%q) = %q, want %q", raw, got, want)
		}
	}
	if webLink("javascript:alert(1)") != "" || webLink("https://ko-fi.com/x") == "" {
		t.Error("webLink")
	}
}

func FuzzIndexChunkDecode(f *testing.F) {
	f.Add([]byte(`[]`))
	f.Add([]byte(`[{"name":"a","owner":"b","versions":[{"version_number":"1.0.0","dependencies":["x-y-1.0.0"]}]}]`))
	f.Add([]byte(`[{"versions":[]}]`))
	f.Add([]byte(`{"x":`))
	f.Fuzz(func(t *testing.T, b []byte) {
		names := depNames{index: map[string]uint32{}}
		var pk []wirePackage
		if json.Unmarshal(b, &pk) != nil {
			return
		}
		for i := range pk {
			if len(pk[i].Versions) == 0 {
				continue
			}
			p := slim(&pk[i], &names)
			if p.ID() == "" && len(p.Versions) != len(pk[i].Versions) {
				t.Fatalf("versions lost from %q", b)
			}
		}
	})
}
