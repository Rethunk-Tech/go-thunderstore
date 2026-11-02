package thunderstore

import (
	"crypto/md5" // #nosec G501 -- checks the Content-MD5 the upload sends
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Rethunk-Tech/go-safefs"
)

// A fake Thunderstore that splits a 10-byte package in two parts, checks each part's bytes and MD5, and records the
// finish and submit bodies.
func TestUploaderFollowsTheCLIProtocol(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "Ab-ShopDrop-0.1.0.zip")
	if err := safefs.WriteFile(pkg, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	var srv *httptest.Server
	var finished, submitted map[string]any
	var auth []string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case r.Method == http.MethodPut:
			sum := md5.Sum(body) // #nosec G401 -- the header under test
			want := map[string]string{"/part/1": "01234", "/part/2": "56789"}[r.URL.Path]
			if string(body) != want || r.Header.Get("Content-MD5") != base64.StdEncoding.EncodeToString(sum[:]) {
				t.Errorf("part %s = %q", r.URL.Path, body)
			}
			w.Header().Set("ETag", `"`+strings.TrimPrefix(r.URL.Path, "/part/")+`"`)
			return
		case strings.HasSuffix(r.URL.Path, "/initiate-upload/"):
			auth = append(auth, r.Header.Get("Authorization"))
			_, _ = fmt.Fprintf(w, `{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"%[1]s/part/1","offset":0,"length":5},{"part_number":2,"url":"%[1]s/part/2","offset":5,"length":5}]}`, srv.URL)
		case strings.HasSuffix(r.URL.Path, "/usermedia/12345678-1234-1234-1234-123456789012/finish-upload/"):
			_ = json.Unmarshal(body, &finished)
		case strings.HasSuffix(r.URL.Path, "/submission/submit/"):
			auth = append(auth, r.Header.Get("Authorization"))
			_ = json.Unmarshal(body, &submitted)
			_, _ = w.Write([]byte(`{"available_communities":[{"url":"https://thunderstore.io/c/lethal-company/p/Ab/ShopDrop/"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	up := NewUploader(srv.Client(), srv.URL, "tok")
	uuid, err := up.Upload(t.Context(), pkg)
	if err != nil || uuid != "12345678-1234-1234-1234-123456789012" {
		t.Fatalf("upload = %q, %v", uuid, err)
	}
	if got, _ := json.Marshal(finished["parts"]); string(got) != `[{"ETag":"\"1\"","PartNumber":1},{"ETag":"\"2\"","PartNumber":2}]` {
		t.Errorf("finished parts = %s", got)
	}
	page, err := up.Submit(t.Context(), uuid, "Ab", "lethal-company", []string{"mods"})
	if err != nil || page != "https://thunderstore.io/c/lethal-company/p/Ab/ShopDrop/" {
		t.Fatalf("submit = %q, %v", page, err)
	}
	if submitted["author_name"] != "Ab" || submitted["upload_uuid"] != "12345678-1234-1234-1234-123456789012" || fmt.Sprint(submitted["community_categories"]) != "map[lethal-company:[mods]]" {
		t.Errorf("submitted = %v", submitted)
	}
	if !slices.Equal(auth, []string{"Bearer tok", "Bearer tok"}) {
		t.Errorf("auth = %q", auth)
	}
}

func TestUploaderRefusesAnHTTPPartURL(t *testing.T) {
	t.Parallel()
	up := NewUploader(nil, "https://thunderstore.io/", "")
	_, err := up.putPart(t.Context(), []byte("abc"), uploadPart{PartNumber: 1, Offset: 0, Length: 3, URL: "http://evil.example/part"})
	if err == nil || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("err = %v, want the http part URL refused", err)
	}
}

func TestUploadRefusesBadInitiateAnswers(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "a.zip")
	if err := safefs.WriteFile(pkg, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, answer := range map[string]string{
		"uuid":    `{"user_media":{"uuid":"../../x"},"upload_urls":[]}`,
		"gap":     `{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"x","offset":0,"length":4}]}`,
		"overlap": `{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"x","offset":0,"length":10},{"part_number":2,"url":"x","offset":5,"length":5}]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/initiate-upload/") {
				_, _ = w.Write([]byte(answer))
			}
		}))
		_, err := NewUploader(srv.Client(), srv.URL, "tok").Upload(t.Context(), pkg)
		srv.Close()
		if err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestErrorBodyIsCut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()
	err := NewUploader(srv.Client(), srv.URL, "tok").call(t.Context(), "p/", map[string]any{}, nil)
	if err == nil || len(err.Error()) > 400 {
		t.Fatalf("err len = %d", len(fmt.Sprint(err)))
	}
}
