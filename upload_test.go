package thunderstore

import (
	"crypto/md5" // #nosec G501 -- checks the Content-MD5 the upload sends
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
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

func TestSubmitRefusesANonHTTPPageURL(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"available_communities":[{"url":"javascript:alert(1)"}]}`))
	}))
	defer srv.Close()
	page, err := NewUploader(srv.Client(), srv.URL, "tok").Submit(t.Context(), "12345678-1234-1234-1234-123456789012", "Ab", "lethal-company", nil)
	if err != nil || page != "" {
		t.Fatalf("submit = %q, %v, want no page and no error", page, err)
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

func TestUploadAbortsWhenStorageIsSplitAcrossHosts(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "a.zip")
	if err := safefs.WriteFile(pkg, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	aborted := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/initiate-upload/"):
			_, _ = w.Write([]byte(`{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"https://s3.example/a","offset":0,"length":5},{"part_number":2,"url":"https://other.example/b","offset":5,"length":5}]}`))
		case strings.Contains(r.URL.Path, "/abort-upload/"):
			aborted = true
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	_, err := NewUploader(srv.Client(), srv.URL, "tok").Upload(t.Context(), pkg)
	if err == nil || !aborted {
		t.Fatalf("err = %v, aborted = %v, want split storage refused and abort-upload", err, aborted)
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

func TestUploaderDoesNotFollowADowngradeWithTheToken(t *testing.T) {
	leaked := false
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked = true }))
	defer plain.Close()
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL, http.StatusTemporaryRedirect)
	}))
	defer tls.Close()
	err := NewUploader(tls.Client(), tls.URL, "secret").call(t.Context(), "p/", map[string]any{}, nil)
	if err == nil || leaked {
		t.Fatalf("err = %v, request reached the plain-http server = %v", err, leaked)
	}
}

func TestUploaderDoesNotFollowAPartRedirectOffTheStorageHost(t *testing.T) {
	pkg := filepath.Join(t.TempDir(), "a.zip")
	if err := safefs.WriteFile(pkg, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	stolen := false
	thief := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		stolen = len(body) > 0
	}))
	defer thief.Close()
	var store *httptest.Server
	store = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			http.Redirect(w, r, thief.URL+"/steal", http.StatusTemporaryRedirect)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/initiate-upload/") {
			_, _ = fmt.Fprintf(w, `{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"%s/part/1","offset":0,"length":10}]}`, store.URL)
		}
	}))
	defer store.Close()
	pool := x509.NewCertPool()
	pool.AddCert(store.Certificate())
	pool.AddCert(thief.Certificate())
	hc := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	_, err := NewUploader(hc, store.URL, "tok").Upload(t.Context(), pkg)
	if err == nil || stolen {
		t.Fatalf("err = %v, zip reached the other host = %v", err, stolen)
	}
}

// Part bounds from the site are summed and sliced with int64 arithmetic: values that wrap must be refused, not
// reach the slice expression.
func TestUploadRefusesPartBoundsThatOverflow(t *testing.T) {
	const huge = int64(1<<63 - 1)
	parts := []uploadPart{{PartNumber: 1, Offset: 0, Length: 10}, {PartNumber: 2, Offset: 10, Length: huge}, {PartNumber: 3, Offset: -huge + 10 - 1, Length: huge}, {PartNumber: 4, Offset: 8, Length: 2}}
	if err := checkParts(parts, 10); err == nil {
		t.Fatal("checkParts accepted parts whose lengths wrap")
	}
	u := &Uploader{client: http.DefaultClient}
	if _, err := u.putPart(t.Context(), make([]byte, 10), uploadPart{PartNumber: 2, Offset: 10, Length: huge}); err == nil {
		t.Fatal("putPart accepted a part past the end")
	}
}

func TestUploaderChecksWhereTheSiteSendsTheZip(t *testing.T) {
	t.Parallel()
	part := func(u string) uploadPart { return uploadPart{URL: u} }
	tests := []struct {
		name    string
		allowed []string
		parts   []uploadPart
		ok      bool
	}{
		{"one named host", nil, []uploadPart{part("https://s3.example/a"), part("https://s3.example/b")}, true},
		{"the site itself", nil, []uploadPart{part("https://thunderstore.io/a")}, true},
		{"split across hosts", nil, []uploadPart{part("https://s3.example/a"), part("https://other.example/b")}, false},
		{"IP literal", nil, []uploadPart{part("https://169.254.169.254/a")}, false},
		{"IPv6 literal", nil, []uploadPart{part("https://[::1]:9000/a")}, false},
		{"hex IP", nil, []uploadPart{part("https://0x7f000001/a")}, false},
		{"decimal IP", nil, []uploadPart{part("https://2130706433/a")}, false},
		{"short IP", nil, []uploadPart{part("https://127.1/a")}, false},
		{"octal IP", nil, []uploadPart{part("https://0177.0.0.1/a")}, false},
		{"hex-dotted IP", nil, []uploadPart{part("https://0x7f.0x0.0x0.0x1/a")}, false},
		{"hex IMDS", nil, []uploadPart{part("https://0xa9fea9fe/a")}, false},
		{"fullwidth IP", nil, []uploadPart{part("https://１２７.０.０.１/a")}, false},
		{"ideographic digit IP", nil, []uploadPart{part("https://１２７.０.０.１:9000/a")}, false},
		{"localhost", nil, []uploadPart{part("https://localhost/a")}, false},
		{"localhost with port", nil, []uploadPart{part("https://localhost:9000/a")}, false},
		{"localhost subdomain", nil, []uploadPart{part("https://s3.localhost/a")}, false},
		{"on the allow-list", []string{"S3.example"}, []uploadPart{part("https://s3.example:443/a")}, true},
		{"off the allow-list", []string{"s3.example"}, []uploadPart{part("https://evil.example/a")}, false},
	}
	for _, tt := range tests {
		up := NewUploader(nil, "https://thunderstore.io/", "")
		up.AllowStorageHosts(tt.allowed...)
		if err := up.checkStorage(tt.parts); (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok=%t", tt.name, err, tt.ok)
		}
	}
}

func TestRefuseBlockedAddr(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		addr string
		ok   bool
	}{
		{"8.8.8.8:443", true},
		{"127.0.0.1:443", false},
		{"10.0.0.1:443", false},
		{"192.168.1.1:443", false},
		{"172.16.0.1:443", false},
		{"169.254.169.254:80", false},
		{"0.0.0.0:443", false},
		{"[::1]:443", false},
		{"[::]:443", false},
		{"[fe80::1]:443", false},
		{"[fc00::1]:443", false},
		{"[2001:4860:4860::8888]:443", true},
		{"[::ffff:127.0.0.1]:443", false},
		{"100.64.0.1:443", false},
		{"[64:ff9b::1]:443", false},
	} {
		err := refuseBlockedAddr(tt.addr)
		if (err == nil) != tt.ok {
			t.Errorf("%s: err = %v, want ok=%t", tt.addr, err, tt.ok)
		}
	}
}

type recordedTripper struct {
	called atomic.Bool
	next   http.RoundTripper
}

func (r *recordedTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	r.called.Store(true)
	return r.next.RoundTrip(req)
}

func TestUploaderKeepsACustomRoundTripper(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	rec := &recordedTripper{next: srv.Client().Transport}
	err := NewUploader(&http.Client{Transport: rec}, srv.URL, "tok").call(t.Context(), "p/", map[string]any{}, nil)
	if !rec.called.Load() {
		t.Fatal("custom RoundTripper was not called")
	}
	if err != nil {
		t.Fatalf("call: %v", err)
	}
}

func TestUploadAllowsALoopbackProxyToAPublicTarget(t *testing.T) {
	var saw atomic.Bool
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			saw.Store(true)
		}
		w.WriteHeader(http.StatusForbidden)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	up := NewUploader(hc, "https://thunderstore.io/", "")
	_, err = up.putPart(t.Context(), []byte("abc"), uploadPart{PartNumber: 1, Length: 3, URL: "https://8.8.8.8/part"})
	if !saw.Load() {
		t.Fatalf("loopback proxy was not used: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "not a public host") {
		t.Fatalf("public target via loopback proxy refused: %v", err)
	}
}

func TestUploadRefusesAPrivateTargetThroughAPublicProxy(t *testing.T) {
	proxyURL, err := url.Parse("http://8.8.8.8:1")
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	up := NewUploader(hc, "https://thunderstore.io/", "")
	_, err = up.putPart(t.Context(), []byte("abc"), uploadPart{PartNumber: 1, Length: 3, URL: "https://10.0.0.1/part"})
	if err == nil || !strings.Contains(err.Error(), "not a public host") {
		t.Fatalf("err = %v, want private target refused", err)
	}
}

func TestAllowPrivate(t *testing.T) {
	t.Parallel()
	up := NewUploader(nil, "https://thunderstore.io/", "")
	if up.allowPrivate("localhost.localdomain") {
		t.Fatal("unlisted name allowed")
	}
	up.AllowStorageHosts("localhost.localdomain")
	if !up.allowPrivate("localhost.localdomain") {
		t.Fatal("listed name refused")
	}
	if !up.allowPrivate("thunderstore.io") {
		t.Fatal("site host refused")
	}
}

func TestUploadDoesNotDialANameThatResolvesToLoopback(t *testing.T) {
	ips, err := net.DefaultResolver.LookupNetIP(t.Context(), "ip", "localhost.localdomain")
	if err != nil {
		t.Fatal(err)
	}
	loopback := false
	for _, ip := range ips {
		if ip.IsLoopback() {
			loopback = true
			break
		}
	}
	if !loopback {
		t.Fatal("localhost.localdomain did not resolve to loopback")
	}
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dialed atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dialed.Add(1)
			_ = c.Close()
		}
	}()
	pkg := filepath.Join(t.TempDir(), "a.zip")
	if err := safefs.WriteFile(pkg, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listen addr is %T", ln.Addr())
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/initiate-upload/") {
			_, _ = fmt.Fprintf(w, `{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"https://localhost.localdomain:%d/part","offset":0,"length":10}]}`, addr.Port)
		}
	}))
	defer srv.Close()
	_, err = NewUploader(srv.Client(), srv.URL, "tok").Upload(t.Context(), pkg)
	if err == nil || dialed.Load() != 0 {
		t.Fatalf("err = %v, loopback name was dialed %d times", err, dialed.Load())
	}
}

func TestUploadDoesNotDialADisguisedLoopbackPartURL(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var dialed atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			dialed.Add(1)
			_ = c.Close()
		}
	}()
	pkg := filepath.Join(t.TempDir(), "a.zip")
	if err := safefs.WriteFile(pkg, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listen addr is %T", ln.Addr())
	}
	port := addr.Port
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/initiate-upload/") {
			_, _ = fmt.Fprintf(w, `{"user_media":{"uuid":"12345678-1234-1234-1234-123456789012"},"upload_urls":[{"part_number":1,"url":"https://0x7f000001:%d/part","offset":0,"length":10}]}`, port)
		}
	}))
	defer srv.Close()
	_, err = NewUploader(srv.Client(), srv.URL, "tok").Upload(t.Context(), pkg)
	if err == nil || dialed.Load() != 0 {
		t.Fatalf("err = %v, disguised loopback was dialed %d times", err, dialed.Load())
	}
}

func TestPutPartErrorHidesSignedURL(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	up := NewUploader(nil, srv.URL, "")
	_, err := up.putPart(t.Context(), []byte("abc"), uploadPart{PartNumber: 1, Length: 3, URL: srv.URL + "/p?X-Amz-Signature=SECRET"})
	if err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("err = %v, want a failure without the signed URL", err)
	}
}

func FuzzStorageURL(f *testing.F) {
	f.Add("https://s3.example/a", "https://thunderstore.io/")
	f.Add("http://evil.example/part", "https://thunderstore.io/")
	f.Add("http://localhost/p", "http://localhost/")
	f.Add("javascript:alert(1)", "https://thunderstore.io/")
	f.Add("", "https://thunderstore.io/")
	f.Add("https://s3.example/p?X-Amz-Signature=SECRET", "https://thunderstore.io/")
	f.Fuzz(func(t *testing.T, raw, apiBase string) {
		got, err := storageURL(raw, apiBase)
		if err != nil {
			return
		}
		if got != raw {
			t.Fatalf("storageURL rewrote %q to %q", raw, got)
		}
		u, err := url.Parse(got)
		if err != nil || u.Host == "" {
			t.Fatalf("accepted unusable %q", got)
		}
		if u.Scheme == "https" {
			return
		}
		api, aerr := url.Parse(apiBase)
		if aerr != nil || api.Scheme != "http" || u.Host != api.Host || u.Scheme != "http" {
			t.Fatalf("accepted non-https %q with api %q", got, apiBase)
		}
	})
}

func FuzzCheckStorage(f *testing.F) {
	f.Add("https://s3.example/a", "https://s3.example/b")
	f.Add("https://s3.example/a", "https://other.example/b")
	f.Add("https://169.254.169.254/a", "")
	f.Add("https://[::1]:9000/a", "")
	f.Add("https://thunderstore.io/a", "")
	f.Add("https://s3.example:443/a", "")
	f.Fuzz(func(t *testing.T, a, b string) {
		up := NewUploader(nil, "https://thunderstore.io/", "")
		parts := []uploadPart{{URL: a}}
		if b != "" {
			parts = append(parts, uploadPart{URL: b})
		}
		if err := up.checkStorage(parts); err != nil {
			return
		}
		var host string
		for _, p := range parts {
			u, err := url.Parse(p.URL)
			if err != nil || u.Host == "" {
				t.Fatalf("accepted unusable %q", p.URL)
			}
			if host == "" {
				host = u.Host
			}
			if u.Host != host {
				t.Fatalf("accepted split hosts %q and %q", host, u.Host)
			}
			name := strings.ToLower(strings.TrimSuffix(hostOnly(u.Host), "."))
			if _, err := netip.ParseAddr(name); err == nil && u.Host != "thunderstore.io" {
				t.Fatalf("accepted IP upload host %q", p.URL)
			}
		}
	})
}
