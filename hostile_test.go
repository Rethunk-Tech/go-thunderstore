package thunderstore

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type hostileListing struct{ index, chunk []byte }

// FuzzHostileListing holds that no index or chunk body, raw or gzipped, makes a build or a read of what it stored
// panic. "SRV" in the index is the fake server's address, so an input can name a chunk the client will fetch. One
// server serves the whole run: a server per input runs the machine out of ports.
func FuzzHostileListing(f *testing.F) {
	f.Add([]byte(`["SRV/chunk"]`), []byte(`[{"name":"a","owner":"b","versions":[{"version_number":"1.0.0","dependencies":["x-y-1.0.0"]}]}]`), false)
	f.Add([]byte(`["SRV/chunk","SRV/chunk"]`), []byte(`[{"name":"a","owner":"b","versions":[]},{"name":"A","owner":"B","versions":[{}]}]`), true)
	f.Add([]byte(`["SRV/chunk"]`), []byte(strings.Repeat("[", 20000)), true)
	f.Add([]byte(`["SRV/chunk"]`), []byte(`[{"versions":[{"dependencies":["","-","a-b-c-d-e"]}]}]`), false)
	f.Add([]byte(`"SRV/chunk"`), []byte(`[]`), false)
	var cur atomic.Pointer[hostileListing]
	cur.Store(&hostileListing{})
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	f.Cleanup(srv.Close)
	mux.HandleFunc("/c/"+community+"/api/v1/package-listing-index/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(cur.Load().index)
	})
	mux.HandleFunc("/chunk", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(cur.Load().chunk) })
	f.Fuzz(func(t *testing.T, index, chunk []byte, zipped bool) {
		index = bytes.ReplaceAll(index, []byte("SRV"), []byte(srv.URL))
		if zipped {
			chunk = gzipped(chunk)
		}
		cur.Store(&hostileListing{index: index, chunk: chunk})
		c, err := New(Config{CacheDir: t.TempDir(), BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Wait()
		idx, err := c.Index(t.Context(), community)
		if err != nil {
			return
		}
		idx.Each(func(p *Package) { _ = p.ID() })
		_, _, _ = c.Find(t.Context(), community, "b", "a")
		_, _ = c.Versions(t.Context(), community, "b", "a")
	})
}

func gzipped(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func TestALyingContentLengthFailsTheBuild(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		conn, rw, err := http.NewResponseController(w).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 1099511627776\r\n\r\n[]")
		_ = rw.Flush()
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{CacheDir: t.TempDir(), BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Index(t.Context(), community); err == nil {
		t.Fatal("an index cut short of its Content-Length was accepted")
	}
}
