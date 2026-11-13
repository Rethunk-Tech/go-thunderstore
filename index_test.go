package thunderstore

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDepCompactorSharesIdenticalListsAndKeepsTheDependencies(t *testing.T) {
	t.Parallel()
	deps := []string{"BepInEx-BepInExPack-5.4.2100", "Owner-Lib-1.0.0"}
	names := depNames{index: map[string]uint32{}}
	mk := func(owner string) Package {
		return Package{
			Owner: owner, Categories: []string{"Tools", "Client-side"},
			Versions: []Version{{Number: "1.0.0", deps: names.ids(deps)}, {Number: "1.0.1", deps: names.ids(deps)}},
		}
	}
	c := newDepCompactor()
	a, b := mk("A"), mk("B")
	c.add(&a)
	c.add(&b)
	c.tab.names = names.names
	if len(names.names) != len(deps) {
		t.Errorf("names = %v, want each dependency once", names.names)
	}
	if a.Versions[0].deps != b.Versions[1].deps || len(c.lists) != 1 {
		t.Error("identical dependency lists must share one encoding")
	}
	if &a.Categories[0] != &b.Categories[0] || a.Categories[0] != "Tools" {
		t.Error("identical category lists must share one slice")
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

// listingBody is a stored listing of pkgs packages, each with one version that names the next deps dependency names of
// a table every file carries; withDeps false omits the dependency lists.
func listingBody(pkgs, deps int, withDeps bool) string {
	var names []string
	for i := range 1 << 14 {
		names = append(names, "Owner-Dep"+strconv.Itoa(i)+"-1.0.0")
	}
	var b strings.Builder
	b.WriteByte('[')
	for i := range pkgs {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"owner":"O%d","name":"P%d","versions":[{"v":"1.0.%d"`, i, i, i)
		if withDeps {
			b.WriteString(`,"d":[`)
			for j := range deps {
				if j > 0 {
					b.WriteByte(',')
				}
				b.WriteString(strconv.Itoa(128 + (i+j)%10000))
			}
			b.WriteByte(']')
		}
		b.WriteString("}]}")
	}
	b.WriteString("]\n[")
	for i, n := range names {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Quote(n))
	}
	b.WriteString("]\n")
	return b.String()
}

// retainedHeap is the live heap after a collection, the size a memo's entry keeps for listingIdle.
func retainedHeap(t *testing.T) uint64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// retainedOf is what reading path keeps after a collection; fn sees the packages while they are measured.
func retainedOf(t *testing.T, path string, fn func([]Package)) uint64 {
	t.Helper()
	before := retainedHeap(t)
	pk, err := readPackages(path)
	if err != nil {
		t.Fatal(err)
	}
	held := retainedHeap(t) - before
	fn(pk)
	runtime.KeepAlive(pk)
	return held
}

// The indexes a version holds survive the varint form at every width it takes, and the stored JSON still reads as
// uint32s.
func TestVersionDependencyIndexesSurviveTheVarintForm(t *testing.T) {
	t.Parallel()
	ids := []uint32{0, 1, 127, 128, 16383, 16384, 1 << 21, math.MaxUint32}
	if got := decodeIDs(encodeIDs(ids)); !slices.Equal(got, ids) {
		t.Fatalf("decodeIDs(encodeIDs(%v)) = %v", ids, got)
	}
	var each []uint32
	eachID(encodeIDs(ids), func(id uint32) { each = append(each, id) })
	if !slices.Equal(each, ids) {
		t.Fatalf("eachID = %v, want %v", each, ids)
	}
	b, err := Version{Number: "1.0.0", deps: encodeIDs(ids)}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var v Version
	if err := v.UnmarshalJSON(b); err != nil || v.deps != encodeIDs(ids) {
		t.Fatalf("%s: %+v, %v", b, v, err)
	}
	if empty, err := (Version{Number: "1.0.0"}).MarshalJSON(); err != nil || string(empty) != `{"v":"1.0.0"}` {
		t.Fatalf("a version with no dependencies writes %s, %v", empty, err)
	}
}

// A memoised listing holds each dependency list once, as two-byte varints: the same indexes as uint32 slices would
// keep twice the bytes for as long as a large community's listing is held.
func TestAReadListingRetainsDependencyIndexesAsVarints(t *testing.T) {
	const pkgs, deps = 3000, 120
	full := writeListing(t, listingBody(pkgs, deps, true))
	lean := writeListing(t, listingBody(pkgs, deps, false))
	count := func(pk []Package) {
		if len(pk) != pkgs {
			t.Fatalf("read %d packages, want %d", len(pk), pkgs)
		}
	}
	whole, none := retainedOf(t, full, count), retainedOf(t, lean, count)
	if whole <= none {
		t.Fatalf("the fixture with dependency lists retains %d bytes, the one without %d: it must retain more", whole, none)
	}
	// 120 indexes cost 480 bytes a version as uint32s and 240 as varints: 1.4 MB against 720 KB over the fixture.
	if indexes := whole - none; indexes > 1_100_000 {
		t.Fatalf("dependency indexes retain %d bytes, want the 720000 of their varint form", indexes)
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

func TestSlimKeepsEveryVersion(t *testing.T) {
	t.Parallel()
	var w wirePackage
	if err := json.Unmarshal([]byte(`{"owner":"A","name":"B","versions":[{"version_number":"2.0.0"},{"version_number":"1.0.0"}]}`), &w); err != nil {
		t.Fatal(err)
	}
	p := slim(&w, &depNames{index: map[string]uint32{}})
	if len(p.Versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(p.Versions))
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
			if len(p.Versions) != len(pk[i].Versions) {
				t.Fatalf("versions lost from %q", b)
			}
		}
	})
}
