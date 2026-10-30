package thunderstore

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	stale := filepath.Join(dir, community+"-c1-0123abcd.json")
	otherKey := filepath.Join(dir, community+"-custom-c2-0123abcd.json")
	crashed := filepath.Join(dir, community+"-c4-0123abcd.json.987.tmp")
	otherTemp := filepath.Join(dir, community+"-custom-c4-0123abcd.json.987.tmp")
	for _, p := range []string{stale, otherKey, crashed, otherTemp} {
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
	for _, kept := range []string{otherKey, otherTemp} {
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
