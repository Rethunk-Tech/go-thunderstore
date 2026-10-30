package thunderstore

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestVersionsAnswerFromTheSideFileWithoutTheListing(t *testing.T) {
	f := newFake(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := f.client(t, func(cfg *Config) { cfg.Now = func() time.Time { return clock } })

	first, err := c.Versions(t.Context(), community, "Alice", "MoreCompany")
	if err != nil || !slices.Equal(first, []string{"2.0.0", "1.0.0"}) {
		t.Fatalf("first = %v, %v", first, err)
	}
	listings, _ := filepath.Glob(filepath.Join(c.dir(), community+"-"+listingTag+"-*.json"))
	if len(listings) != 1 {
		t.Fatalf("listings = %v", listings)
	}
	// With the listing gone only the side file can answer.
	if err := os.Remove(listings[0]); err != nil {
		t.Fatal(err)
	}
	hits := f.indexHits.Load()
	again, err := c.Versions(t.Context(), community, "alice", "morecompany")
	if err != nil || !slices.Equal(again, first) || f.indexHits.Load() != hits {
		t.Fatalf("from the side file = %v, %v; index hits %d -> %d", again, err, hits, f.indexHits.Load())
	}
	if _, err := os.Stat(listings[0]); !os.IsNotExist(err) {
		t.Fatalf("the listing was rebuilt: %v", err)
	}

	// More than an hour on, the side file still answers while the refresh runs behind it; the new list shows once the
	// refresh has rebuilt the listing.
	f.chunk0[0]["versions"] = []ver{{Description: "d", VersionNumber: "3.0.0"}, {Description: "d", VersionNumber: "2.0.0"}, {Description: "d", VersionNumber: "1.0.0"}}
	f.third.Store(true)
	clock = clock.Add(2 * refreshAfter)
	stale, err := c.Versions(t.Context(), community, "Alice", "MoreCompany")
	if err != nil || !slices.Equal(stale, first) {
		t.Fatalf("a stale side file = %v, %v; want %v", stale, err, first)
	}
	waitFor(t, func() bool {
		got, err := c.Versions(t.Context(), community, "Alice", "MoreCompany")
		return err == nil && len(got) == 3
	})
}

func TestVersionsOnAColdCacheLeavesNoListingInMemory(t *testing.T) {
	f := newFake(t)
	c := f.client(t, nil)
	got, err := c.Versions(t.Context(), community, "Alice", "MoreCompany")
	if err != nil || len(got) == 0 {
		t.Fatalf("versions = %v, %v", got, err)
	}
	if len(c.memo) != 0 {
		t.Fatal("a version lookup kept the whole listing in memory")
	}
	if _, err := c.Versions(t.Context(), community, "Nobody", "Nothing"); err == nil {
		t.Fatal("a package the index lacks had versions")
	}
}
