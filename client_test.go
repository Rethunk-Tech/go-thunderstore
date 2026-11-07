package thunderstore

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Rethunk-Tech/go-safefs"
)

func TestNewNeedsACacheDir(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("a Client without a cache folder was built")
	}
}

func TestIndexKeepsTheWireFieldsAConsumerNeeds(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	x, err := c.Index(t.Context(), community)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Packages()) != 3 {
		t.Fatalf("packages = %d", len(x.Packages()))
	}
	p, ok := x.Find("alice", "morecompany")
	if !ok || p.Repo != "Alice/MoreCompany" || p.Donation != "https://ko-fi.com/Alice" || p.Adult || p.Downloads != 501 || p.Versions[0].Size != 99 {
		t.Fatalf("package = %+v", p)
	}
	if got := p.Dependencies(p.Versions[0]); !slices.Equal(got, []string{"BepInEx-BepInExPack-5.4.2100"}) || p.Dependencies(p.Versions[1]) != nil {
		t.Fatalf("deps = %v", got)
	}
	if p.Versions[0].Created != 1768471200 {
		t.Fatalf("created = %d", p.Versions[0].Created)
	}
	if o, _ := x.Find("Eve", "OldMod"); !o.Deprecated {
		t.Fatal("deprecated flag lost")
	}
	if s, _ := x.Find("Carol", "Spicy"); !s.Adult {
		t.Fatal("adult flag lost")
	}
	if _, ok := x.Find("nobody", "nothing"); ok {
		t.Fatal("found a package the index lacks")
	}
}

func TestCacheHitThenRefresh(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	c := f.client(t, func(cfg *Config) { cfg.Now = func() time.Time { return now } })
	if _, err := c.Index(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	now = now.Add(30 * time.Minute)
	if _, err := c.Index(t.Context(), community); err != nil || f.indexHits.Load() != 1 || f.chunkHit.Load() != 2 {
		t.Fatalf("hit: err %v index %d chunks %d", err, f.indexHits.Load(), f.chunkHit.Load())
	}
	now = now.Add(time.Hour)
	if _, err := c.Index(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return f.indexHits.Load() == 2 })
	c.Wait()
	if f.chunkHit.Load() != 2 {
		t.Fatalf("an unchanged index must not refetch chunks: %d", f.chunkHit.Load())
	}
}

func TestChangedIndexIsServedStaleThenAnnounced(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	told := make(chan string, 1)
	c := f.client(t, func(cfg *Config) {
		cfg.Now = func() time.Time { return now }
		cfg.Refreshed = func(key string) { told <- key }
	})
	if x, _ := c.Index(t.Context(), community); len(x.Packages()) != 3 {
		t.Fatal("first build")
	}
	f.third.Store(true)
	now = now.Add(2 * time.Hour)
	if x, err := c.Index(t.Context(), community); err != nil || len(x.Packages()) != 3 {
		t.Fatalf("the stale listing must answer at once: %v", err)
	}
	select {
	case key := <-told:
		if key != community {
			t.Fatalf("told about %q", key)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the refresh that changed the listing was not announced")
	}
	if x, err := c.Index(t.Context(), community); err != nil || len(x.Packages()) != 4 {
		t.Fatalf("after the refresh: %v", err)
	}
}

func TestRefreshedIsSilentWhenTheIndexIsUnchanged(t *testing.T) {
	f := newFake(t)
	told := make(chan string, 1)
	c := f.client(t, func(cfg *Config) { cfg.Refreshed = func(key string) { told <- key } })
	if _, err := c.Index(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	c.Refresh(community)
	waitFor(t, func() bool { return f.indexHits.Load() == 2 })
	c.Wait()
	select {
	case key := <-told:
		t.Fatalf("told about %q though nothing changed", key)
	default:
	}
}

func TestIndexCannotSendTheClientElsewhere(t *testing.T) {
	f := newFake(t)
	f.evil.Store(true)
	c := f.client(t, nil)
	if _, err := c.Index(t.Context(), community); err == nil {
		t.Fatal("followed a chunk URL on another host")
	}
	if _, err := c.Index(t.Context(), "../../etc"); err == nil {
		t.Fatal("accepted a community key that is a path")
	}
}

func TestAnOversizeChunkIsRefusedNotCutShort(t *testing.T) {
	t.Parallel()
	body := append([]byte("["), bytes.Repeat([]byte(" "), maxBody)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	c, _ := New(Config{CacheDir: t.TempDir()})
	if _, err := c.get(t.Context(), srv.URL); !errors.Is(err, safefs.ErrTooLarge) {
		t.Fatalf("get: %v", err)
	}
	err := c.streamPackages(t.Context(), srv.URL, func(*wirePackage) error { return nil })
	if !errors.Is(err, safefs.ErrTooLarge) {
		t.Fatalf("stream: %v", err)
	}
}

func TestAGzipBombIsBoundedByMaxUnpacked(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(gz(t, []string{string(make([]byte, 4096))}))
	}))
	defer srv.Close()
	c, _ := New(Config{CacheDir: t.TempDir()})
	c.maxUnpacked = 1024
	if _, err := c.getJSON(t.Context(), srv.URL, new([]string)); !errors.Is(err, safefs.ErrTooLarge) {
		t.Fatalf("getJSON: %v", err)
	}
	err := c.streamPackages(t.Context(), srv.URL, func(*wirePackage) error { return nil })
	if !errors.Is(err, safefs.ErrTooLarge) {
		t.Fatalf("stream: %v", err)
	}
}

func TestABuildRemovesTheCommunitysOtherListings(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	dir := c.dir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, community+"-c1-"+hash64+".json")
	otherKey := filepath.Join(dir, community+"-custom-c2-"+hash64+".json")
	crashed := filepath.Join(dir, community+"-c4-"+hash64+".json.987.tmp")
	otherTemp := filepath.Join(dir, community+"-custom-c4-"+hash64+".json.987.tmp")
	// A community whose key is this one plus "-c4" names its listings "<key>-c4-c4-<hash>.json".
	sibling := filepath.Join(dir, community+"-c4-"+listingTag+"-"+hash64+".json")
	for _, p := range []string{stale, otherKey, crashed, otherTemp, sibling} {
		if err := os.WriteFile(p, []byte("[]"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Index(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{stale, crashed} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s stayed: %v", gone, err)
		}
	}
	for _, kept := range []string{otherKey, otherTemp, sibling} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("another community's file was removed: %v", err)
		}
	}
}

func TestBuiltHandsOverTheStoredListing(t *testing.T) {
	f := newFake(t)
	var path string
	c := f.client(t, func(cfg *Config) { cfg.Built = func(_, p string) { path = p } })
	if _, err := c.Index(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := EachStored(path, func(*Package) { n++ }); err != nil || n != 3 {
		t.Fatalf("EachStored = %d, %v", n, err)
	}
}

func TestEachAndFindLeaveNoListingInMemory(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	n := 0
	if err := c.Each(t.Context(), community, func(*Package) { n++ }); err != nil || n != 3 {
		t.Fatalf("each = %d, %v", n, err)
	}
	p, ok, err := c.Find(t.Context(), community, "ALICE", "morecompany")
	if err != nil || !ok || p.Name != "MoreCompany" {
		t.Fatalf("find = %+v, %v, %v", p, ok, err)
	}
	if len(c.memo) != 0 {
		t.Fatal("a streamed read kept the whole listing in memory")
	}
}

func TestStoredNeverTouchesTheNetwork(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	c := f.client(t, func(cfg *Config) { cfg.Now = func() time.Time { return now } })
	count := func() (n int) {
		ok, err := c.Stored(t.Context(), community, func(*Package) { n++ })
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			return -1
		}
		return n
	}
	if got := count(); got != -1 || f.indexHits.Load() != 0 || f.chunkHit.Load() != 0 {
		t.Fatalf("cold: n %d, index %d chunks %d", got, f.indexHits.Load(), f.chunkHit.Load())
	}
	if _, err := c.Stored(t.Context(), "Bad Key", func(*Package) {}); err == nil {
		t.Fatal("a bad community key was accepted")
	}

	if _, err := c.ListingFile(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * refreshAfter)
	if got := count(); got != 3 {
		t.Fatalf("stale: n %d", got)
	}
	c.Wait()
	if f.indexHits.Load() != 1 || f.chunkHit.Load() != 2 {
		t.Fatalf("a stale read reached out: index %d chunks %d", f.indexHits.Load(), f.chunkHit.Load())
	}
	if c.Held(community) {
		t.Fatal("Stored kept the listing in memory")
	}
	if v, ok, err := c.StoredVersions(community, "Alice", "MoreCompany"); err != nil || !ok || !slices.Equal(v, []string{"2.0.0", "1.0.0"}) {
		t.Fatalf("stored versions = %v %v %v", v, ok, err)
	}
	if _, ok, _ := c.StoredVersions(community, "Nobody", "Nothing"); ok {
		t.Fatal("versions for a package the listing lacks")
	}
	c.Wait()
	if f.indexHits.Load() != 1 {
		t.Fatalf("StoredVersions reached out: %d", f.indexHits.Load())
	}
}

func TestStoredUsesTheHeldListing(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	if c.Held(community) {
		t.Fatal("held before anything read")
	}
	if _, err := c.Index(t.Context(), community); err != nil {
		t.Fatal(err)
	}
	if !c.Held(community) {
		t.Fatal("Index did not hold the listing")
	}
	var deps int
	ok, err := c.Stored(t.Context(), community, func(p *Package) { deps += len(p.Dependencies(p.Versions[0])) })
	if err != nil || !ok || deps != 3 {
		t.Fatalf("held read: ok %v err %v deps %d (memory copy carries dependencies)", ok, err, deps)
	}
}

func TestSnapshotAndEachPreferTheHeldListing(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	x, err := c.Snapshot(t.Context(), community)
	if err != nil || len(x.Packages()) != 3 || c.Held(community) {
		t.Fatalf("snapshot: %v, held %v", err, c.Held(community))
	}
	held, err := c.Index(t.Context(), community)
	if err != nil {
		t.Fatal(err)
	}
	if x, err = c.Snapshot(t.Context(), community); err != nil || x != held {
		t.Fatalf("snapshot of a held listing: %v, same copy %v", err, x == held)
	}
	n := 0
	if err := c.Each(t.Context(), community, func(p *Package) { n += len(p.Dependencies(p.Versions[0])) }); err != nil || n != 3 {
		t.Fatalf("each from memory: %d deps, %v", n, err)
	}
	for name, err := range map[string]error{
		"Snapshot": func() error { _, err := c.Snapshot(t.Context(), "Bad Key"); return err }(),
		"Each":     c.Each(t.Context(), "Bad Key", func(*Package) {}),
		"Find":     func() error { _, _, err := c.Find(t.Context(), "Bad Key", "a", "b"); return err }(),
	} {
		if err == nil {
			t.Errorf("%s accepted a bad community key", name)
		}
	}
}

const hash64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func TestAChunkRedirectCannotLeaveTheTrustedHosts(t *testing.T) {
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		elsewhere.Add(1)
		_, _ = w.Write([]byte("[]"))
	}))
	defer other.Close()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/chunk/") {
			http.Redirect(w, r, other.URL, http.StatusFound)
			return
		}
		_, _ = w.Write(gz(t, []string{base + "/chunk/0"}))
	}))
	defer srv.Close()
	base = srv.URL
	c, _ := New(Config{CacheDir: t.TempDir(), BaseURL: base})
	if _, err := c.Index(t.Context(), community); err == nil {
		t.Fatal("followed a chunk redirect to another host")
	}
	if elsewhere.Load() != 0 {
		t.Fatalf("the other host was requested %d times", elsewhere.Load())
	}
}

func TestABuildPastTheListingCapFails(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	c.maxListing = 100
	if _, err := c.Index(t.Context(), community); !errors.Is(err, safefs.ErrTooLarge) {
		t.Fatalf("Index: %v", err)
	}
}

func TestAMetaHashThatIsNotAHashIsIgnored(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	if err := os.MkdirAll(c.dir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.metaPath(community), []byte(`{"hash":"../../x","fetched":"2099-01-01T00:00:00Z"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if h := c.readMeta(community).Hash; h != "" {
		t.Fatalf("hash %q accepted", h)
	}
}

func TestAListingGoneBetweenLookupAndOpenIsLookedUpAgain(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	first, err := c.ListingFile(t.Context(), community)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	err = c.onListing(t.Context(), community, func(path string) error {
		calls++
		if calls == 1 {
			return &fs.PathError{Op: "open", Path: first, Err: fs.ErrNotExist}
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err = %v, calls = %d", err, calls)
	}
}
