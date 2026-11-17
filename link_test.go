package thunderstore

import (
	"net/url"
	"slices"
	"strings"
	"testing"
)

func TestParseLink(t *testing.T) {
	r, err := ParseLink("ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/")
	if err != nil || r != (Ref{"Alice", "MoreCompany", "2.0.0"}) {
		t.Fatalf("%+v %v", r, err)
	}
	for _, bad := range []string{
		"https://thunderstore.io/x", "ror2mm://v2/install/thunderstore.io/A/B/1.0.0/", "ror2mm://v1/install/evil.io/A/B/1.0.0/",
		"ror2mm://v1/install/thunderstore.io/A/B/latest/", "ror2mm://v1/install/thunderstore.io/A/../1.0.0/",
		"ror2mm://v1/install/thunderstore.io/A/B/1.0.0/?x=1", "ror2mm://u@v1/install/thunderstore.io/A/B/1.0.0/",
	} {
		if _, err := ParseLink(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestParseLinkRefusesEscapesQueryAndFragment(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"ror2mm://v1/install/thunderstore.io/Alice/More%43ompany/2.0.0/",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/1%2e0%2e0/",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/?",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/#",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/?x=1",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/#frag",
		"ror2mm://u@v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/",
	} {
		if ref, err := ParseLink(raw); err == nil {
			t.Fatalf("%q gave %+v", raw, ref)
		}
	}
	if ref, err := ParseLink("ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/"); err != nil || ref.Name != "MoreCompany" || ref.Version != "2.0.0" {
		t.Fatalf("plain link: %+v %v", ref, err)
	}
}

func FuzzParseLink(f *testing.F) {
	for _, raw := range []string{
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/",
		"ror2mm://v1/install/thunderstore.io/Ns/Name/1.2.3/",
		"https://thunderstore.io/x",
		"ror2mm://v2/install/thunderstore.io/A/B/1.0.0/",
		"ror2mm://v1/install/evil.io/A/B/1.0.0/",
		"ror2mm://v1/install/thunderstore.io/A/B/latest/",
		"ror2mm://v1/install/thunderstore.io/A/../1.0.0/",
		"ror2mm://v1/install/thunderstore.io/A/B/1.0.0/?x=1",
		"ror2mm://u@v1/install/thunderstore.io/A/B/1.0.0/",
		"ror2mm://v1/install/thunderstore.io/../x/1.0.0/",
		"ror2mm://v1/install/thunderstore.io/a/b/c/",
		"ror2mm://v1/install/thunderstore.io/Alice/More%43ompany/2.0.0/",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/1%2e0%2e0/",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/?",
		"ror2mm://v1/install/thunderstore.io/Alice/MoreCompany/2.0.0/#",
		"",
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		ref, err := ParseLink(raw)
		if err != nil {
			return
		}
		for _, s := range []string{ref.Namespace, ref.Name, ref.Version} {
			if !strings.Contains(raw, s) {
				t.Fatalf("unbounded field %q from %q", s, raw)
			}
		}
		if !part.MatchString(ref.Namespace) || !part.MatchString(ref.Name) || !versionRe.MatchString(ref.Version) {
			t.Fatalf("bad ref %+v from %q", ref, raw)
		}
		again := Scheme + "://v1/install/thunderstore.io/" + ref.Namespace + "/" + ref.Name + "/" + ref.Version + "/"
		got, err := ParseLink(again)
		if err != nil || got != ref {
			t.Fatalf("round-trip %+v from %q: %+v %v", ref, raw, got, err)
		}
	})
}

func FuzzOnHost(f *testing.F) {
	for _, raw := range []string{
		"https://b.example/live/repository/packages/Me-Mod-1.0.0.zip?x=1",
		"javascript:alert(1)", "data:text/html,hi", "file:///etc/passwd", "/relative/path",
		"http://ccdn.thunderstore.io/x", "https://gcdn.thunderstore.io/a/b",
		"", "https://", "https://user:pass@host/path",
	} {
		f.Add(raw, "https://ccdn.thunderstore.io")
	}
	f.Add("https://b.example/x.zip", "https://c.example")
	f.Fuzz(func(t *testing.T, raw, base string) {
		got, ok := OnHost(raw, base)
		if !ok {
			return
		}
		u, err := url.Parse(got)
		b, berr := url.Parse(base)
		if err != nil || berr != nil || u.Host == "" || u.Host != b.Host || u.Scheme != b.Scheme {
			t.Fatalf("OnHost(%q, %q) = %q", raw, base, got)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Fatalf("rewrote to non-http %q", got)
		}
	})
}

func TestCDNOrder(t *testing.T) {
	hosts := []string{"https://a.example", "https://b.example", "https://c.example"}
	raw := "https://b.example/live/repository/packages/Me-Mod-1.0.0.zip?x=1"
	for name, c := range map[string]struct {
		pinned, last string
		want         []string
	}{
		"automatic starts at the first": {"", "", hosts},
		"the last good host leads":      {"", hosts[2], []string{hosts[2], hosts[0], hosts[1]}},
		"a pinned host stands alone":    {hosts[1], hosts[2], []string{hosts[1]}},
		"an unknown pin is automatic":   {"https://z.example", "", hosts},
	} {
		if got := CDNOrder(raw, hosts, c.pinned, c.last); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	if got, _ := OnHost(raw, hosts[2]); got != "https://c.example/live/repository/packages/Me-Mod-1.0.0.zip?x=1" {
		t.Errorf("OnHost = %q", got)
	}
	if CDNOrder("https://elsewhere.example/x.zip", hosts, "", "") != nil {
		t.Error("an address off the CDNs was given alternatives")
	}
}

func TestOnHostRefusesNonHTTPAddresses(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,hi", "file:///etc/passwd", "/relative/path"} {
		if got, ok := OnHost(raw, "https://ccdn.thunderstore.io"); ok {
			t.Errorf("OnHost(%q) = %q, want it refused", raw, got)
		}
	}
}
