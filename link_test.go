package thunderstore

import (
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

func FuzzParseLink(f *testing.F) {
	f.Add("ror2mm://v1/install/thunderstore.io/Ns/Name/1.2.3/")
	f.Add("ror2mm://v1/install/thunderstore.io/../x/1.0.0/")
	f.Add("ror2mm://v1/install/thunderstore.io/a/b/c/")
	f.Add("")
	f.Fuzz(func(t *testing.T, raw string) {
		ref, err := ParseLink(raw)
		if err != nil {
			return
		}
		for _, s := range []string{ref.Namespace, ref.Name} {
			if !part.MatchString(s) {
				t.Fatalf("bad part %q from %q", s, raw)
			}
		}
		if !versionRe.MatchString(ref.Version) || strings.Contains(ref.Namespace+ref.Name, "/") {
			t.Fatalf("bad ref %+v from %q", ref, raw)
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
