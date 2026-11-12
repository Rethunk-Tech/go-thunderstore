package thunderstore

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"

	"github.com/Rethunk-Tech/go-httpkit"
)

func statusServer(t *testing.T, code int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", code)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPFailuresAreTyped(t *testing.T) {
	t.Parallel()
	pkg := t.TempDir() + "/a.zip"
	if err := os.WriteFile(pkg, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{http.StatusUnauthorized, http.StatusNotFound} {
		srv := statusServer(t, code)
		c, err := New(Config{CacheDir: t.TempDir(), BaseURL: srv.URL})
		if err != nil {
			t.Fatal(err)
		}
		_, cerr := c.Categories(t.Context(), community)
		_, uerr := NewUploader(srv.Client(), srv.URL, "t").Upload(t.Context(), pkg)
		for name, err := range map[string]error{"client": cerr, "uploader": uerr} {
			var se *httpkit.StatusError
			if !errors.As(err, &se) || se.Code != code {
				t.Errorf("%s %d: err = %v, want a *httpkit.StatusError", name, code, err)
			}
			if got := errors.Is(err, httpkit.ErrNotFound); got != (code == http.StatusNotFound) {
				t.Errorf("%s %d: Is(ErrNotFound) = %v", name, code, got)
			}
		}
	}
}

func TestCategories(t *testing.T) {
	t.Parallel()
	body, err := os.ReadFile("testdata/community-filters.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/cyberstorm/community/"+community+"/filters/" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{CacheDir: t.TempDir(), BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Categories(t.Context(), community)
	want := []Category{{"tools", "Tools"}, {"mods", "Mods"}, {"modpacks", "Modpacks"}, {"tweaks-and-quality-of-life", "Tweaks & Quality Of Life"}, {"ai-generated", "AI Generated"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("Categories = %v, %v", got, err)
	}
	if _, err := c.Categories(t.Context(), "../x"); err == nil {
		t.Error("a bad community key must be refused")
	}
}

func TestParseDependency(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]Dependency{
		"BepInEx-BepInExPack-5.4.2100": {"BepInEx", "BepInExPack", "5.4.2100"},
		"Ab_c1-My_Mod_2-0.1.0-beta.1":  {},
		"x-y-1.0.0":                    {"x", "y", "1.0.0"},
		"a-b-c-1.0.0":                  {},
		"Team-Name-../../x?y":          {},
		"Te/am-Name-1.0.0":             {},
		"Team-Na.me-1.0.0":             {},
		"-Name-1.0.0":                  {},
		"NS--1.0.0":                    {},
		"NS-Name-":                     {},
		"NS-1.0.0":                     {},
		"plain":                        {},
		"":                             {},
		"Ab_c1-My_Mod_2-10.20.30":      {"Ab_c1", "My_Mod_2", "10.20.30"},
	} {
		d, err := ParseDependency(in)
		if (err == nil) != (want != Dependency{}) || d != want {
			t.Errorf("ParseDependency(%q) = %v, %v; want %v", in, d, err, want)
		}
		if err == nil && d.String() != in {
			t.Errorf("String() = %q, want %q", d.String(), in)
		}
	}
}

func FuzzParseDependency(f *testing.F) {
	for _, in := range []string{
		"BepInEx-BepInExPack-5.4.2100",
		"Ab_c1-My_Mod_2-0.1.0-beta.1",
		"x-y-1.0.0",
		"a-b-c-1.0.0",
		"Team-Name-../../x?y",
		"Te/am-Name-1.0.0",
		"Team-Na.me-1.0.0",
		"-Name-1.0.0",
		"NS--1.0.0",
		"NS-Name-",
		"NS-1.0.0",
		"plain",
		"",
		"Ab_c1-My_Mod_2-10.20.30",
	} {
		f.Add(in)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := ParseDependency(s)
		if err != nil {
			return
		}
		if !part.MatchString(d.Namespace) || !part.MatchString(d.Name) || !versionRe.MatchString(d.Version) {
			t.Fatalf("bad dependency %+v from %q", d, s)
		}
		if d.String() != s {
			t.Fatalf("unbounded or rearranged %+v from %q: %q", d, s, d.String())
		}
		got, err := ParseDependency(d.String())
		if err != nil || got != d {
			t.Fatalf("round-trip %+v from %q: %+v %v", d, s, got, err)
		}
	})
}
