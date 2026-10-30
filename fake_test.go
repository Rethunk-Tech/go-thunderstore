package thunderstore

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const community = "lethal-company"

func gz(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(raw)
	_ = w.Close()
	return buf.Bytes()
}

type ver struct {
	Description   string   `json:"description"`
	Icon          string   `json:"icon"`
	VersionNumber string   `json:"version_number"`
	DateCreated   string   `json:"date_created"`
	Dependencies  []string `json:"dependencies"`
	Downloads     int      `json:"downloads"`
	WebsiteURL    string   `json:"website_url"`
	FileSize      int64    `json:"file_size"`
}

func listing(owner, name, desc string, downloads int, dep, nsfw bool) map[string]any {
	return map[string]any{
		"name": name, "owner": owner, "package_url": "https://x/p/" + owner + "/" + name + "/", "date_updated": "2026-01-01",
		"date_created": "2025-01-01T00:00:00Z", "rating_score": 3, "is_deprecated": dep, "has_nsfw_content": nsfw,
		"donation_link": "https://ko-fi.com/" + owner, "categories": []string{"Items"},
		"versions": []ver{
			{
				Description: desc, VersionNumber: "2.0.0", DateCreated: "2026-01-15T10:00:00.123456Z", Dependencies: []string{"BepInEx-BepInExPack-5.4.2100"},
				Downloads: downloads, FileSize: 99, WebsiteURL: "https://github.com/" + owner + "/" + name + ".git",
			},
			{Description: desc, VersionNumber: "1.0.0", DateCreated: "2025-12-01T00:00:00Z", Downloads: 1},
		},
	}
}

type fake struct {
	srv                 *httptest.Server
	indexHits, chunkHit atomic.Int32
	chunk0, chunk1      []map[string]any
	third, evil         atomic.Bool
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		chunk0: []map[string]any{
			listing("Alice", "MoreCompany", "more players", 500, false, false),
			listing("Eve", "OldMod", "gone", 99999, true, false),
		},
		chunk1: []map[string]any{listing("Carol", "Spicy", "adult stuff", 7, false, true)},
	}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/c/"+community+"/api/v1/package-listing-index/", func(w http.ResponseWriter, r *http.Request) {
		f.indexHits.Add(1)
		if r.Header.Get("User-Agent") != "test-agent" {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		urls := []string{f.srv.URL + "/chunk/0", f.srv.URL + "/chunk/1"}
		if f.evil.Load() {
			urls = []string{"http://169.254.169.254/latest/meta-data"}
		}
		if f.third.Load() {
			urls = append(urls, f.srv.URL+"/chunk/2")
		}
		_, _ = w.Write(gz(t, urls))
	})
	mux.HandleFunc("/chunk/0", func(w http.ResponseWriter, _ *http.Request) { f.chunkHit.Add(1); _, _ = w.Write(gz(t, f.chunk0)) })
	mux.HandleFunc("/chunk/1", func(w http.ResponseWriter, _ *http.Request) { f.chunkHit.Add(1); _, _ = w.Write(gz(t, f.chunk1)) })
	mux.HandleFunc("/chunk/2", func(w http.ResponseWriter, _ *http.Request) {
		f.chunkHit.Add(1)
		_, _ = w.Write(gz(t, []map[string]any{listing("Zed", "NewOne", "fresh", 1, false, false)}))
	})
	return f
}

// client builds a Client against f in a fresh cache folder and waits for its refreshes before the folder goes.
func (f *fake) client(t *testing.T, mod func(*Config)) *Client {
	t.Helper()
	cfg := Config{CacheDir: t.TempDir(), BaseURL: f.srv.URL, UserAgent: "test-agent"}
	if mod != nil {
		mod(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Wait)
	return c
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting")
		}
	}
}
