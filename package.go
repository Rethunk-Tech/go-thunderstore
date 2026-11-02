package thunderstore

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Rethunk-Tech/go-github"
	"github.com/Rethunk-Tech/go-semverish"
)

// Version is one published version of a package.
type Version struct {
	Number string
	Size   int64
	// Created is when the version was published, as Unix seconds; 0 when the site gave no readable date.
	Created int64
	// deps indexes the listing's dependency names; Package.Dependencies resolves them.
	deps []uint32
}

type storedVersion struct {
	Number  string   `json:"v"`
	Size    int64    `json:"size,omitempty"`
	Created int64    `json:"t,omitempty"`
	Deps    []uint32 `json:"d,omitempty"`
}

// MarshalJSON writes the compact form the stored listing uses.
func (v Version) MarshalJSON() ([]byte, error) {
	return json.Marshal(storedVersion{v.Number, v.Size, v.Created, v.deps})
}

// UnmarshalJSON reads the compact form the stored listing uses.
func (v *Version) UnmarshalJSON(b []byte) error {
	var s storedVersion
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*v = Version{s.Number, s.Size, s.Created, s.Deps}
	return nil
}

// Package is one package listing, its versions newest first as the site orders them.
type Package struct {
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	Summary   string `json:"summary"`
	Icon      string `json:"icon"`
	URL       string `json:"url"`
	Updated   string `json:"updated"`
	Created   string `json:"created"`
	Rating    int    `json:"rating"`
	Downloads int    `json:"downloads"`
	// Repo is the GitHub repository the newest version's website links, as "owner/repo"; empty when it links none.
	Repo string `json:"repo,omitempty"`
	// Donation is the author's donation page when it is an http or https address.
	Donation string `json:"donation,omitempty"`
	// Deprecated packages are still listed so a dependency on one resolves.
	Deprecated bool `json:"hidden,omitempty"`
	// Adult marks a package the site flags as NSFW.
	Adult      bool      `json:"adult,omitempty"`
	Versions   []Version `json:"versions"`
	Categories []string  `json:"categories,omitempty"`
	tab        *depTable
}

// ID is the package's lowercased "owner-name", the key the site treats names by.
func (p Package) ID() string { return packageID(p.Owner, p.Name) }

// Dependencies is the "Owner-Name-1.2.3" strings one of p's versions depends on. A package decoded by Client.Each or
// Client.Find carries none: only an Index holds the names.
func (p Package) Dependencies(v Version) []string {
	if len(v.deps) == 0 || p.tab == nil {
		return nil
	}
	out := make([]string, len(v.deps))
	for i, id := range v.deps {
		out[i] = p.tab.names[id]
	}
	return out
}

// VersionNumbers is the package's version numbers, newest first.
func (p Package) VersionNumbers() []string {
	out := make([]string, len(p.Versions))
	for i, v := range p.Versions {
		out[i] = v.Number
	}
	slices.SortStableFunc(out, func(a, b string) int {
		switch {
		case newer(a, b):
			return -1
		case newer(b, a):
			return 1
		}
		return 0
	})
	return out
}

// Outranks reports whether p wins over q, a case-folded twin listed after it: a live listing over a deprecated one,
// then the more recently updated, then the first.
func (p Package) Outranks(q Package) bool {
	return p.Deprecated != q.Deprecated && q.Deprecated || p.Deprecated == q.Deprecated && p.Updated >= q.Updated
}

func packageID(owner, name string) string { return strings.ToLower(owner + "-" + name) }

// newer reports whether a is a later version than b; either failing to parse means not newer.
func newer(a, b string) bool {
	c, ok := semverish.Compare(a, b)
	return ok && c > 0
}

// wirePackage is a v1 package listing as the site serves it.
type wirePackage struct {
	Name           string   `json:"name"`
	Owner          string   `json:"owner"`
	PackageURL     string   `json:"package_url"`
	DateUpdated    string   `json:"date_updated"`
	DateCreated    string   `json:"date_created"`
	RatingScore    int      `json:"rating_score"`
	IsDeprecated   bool     `json:"is_deprecated"`
	HasNSFWContent bool     `json:"has_nsfw_content"`
	DonationLink   string   `json:"donation_link"`
	Categories     []string `json:"categories"`
	Versions       []struct {
		Description   string   `json:"description"`
		Icon          string   `json:"icon"`
		VersionNumber string   `json:"version_number"`
		DateCreated   string   `json:"date_created"`
		Dependencies  []string `json:"dependencies"`
		Downloads     int      `json:"downloads"`
		WebsiteURL    string   `json:"website_url"`
		FileSize      int64    `json:"file_size"`
	} `json:"versions"`
}

// slim keeps the fields of a wire package a consumer uses.
func slim(w *wirePackage, names *depNames) Package {
	p := Package{
		Owner: w.Owner, Name: w.Name, URL: w.PackageURL, Updated: w.DateUpdated, Created: w.DateCreated, Rating: w.RatingScore,
		Deprecated: w.IsDeprecated, Categories: w.Categories, Adult: w.HasNSFWContent, Summary: w.Versions[0].Description,
		Icon: w.Versions[0].Icon, Repo: githubRepo(w.Versions[0].WebsiteURL), Donation: webLink(w.DonationLink),
	}
	p.Versions = make([]Version, len(w.Versions))
	for i, v := range w.Versions {
		p.Downloads += v.Downloads
		p.Versions[i] = Version{Number: v.VersionNumber, Size: v.FileSize, deps: names.ids(v.Dependencies), Created: released(v.DateCreated)}
	}
	return p
}

// webLink is raw when it is an http or https address with a host, else empty.
func webLink(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return ""
	}
	return raw
}

// githubRepo reads "owner/repo" from a github.com repository address, or "" when raw is not one.
func githubRepo(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if host := strings.ToLower(u.Hostname()); host != "github.com" && host != "www.github.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[0], strings.TrimSuffix(parts[1], ".git")
	if ownerRepo := owner + "/" + name; github.ValidRepo(ownerRepo) {
		return ownerRepo
	}
	return ""
}

// released is an RFC 3339 time as Unix seconds, or 0 when it does not parse.
func released(rfc3339 string) int64 {
	t, err := time.Parse(time.RFC3339, rfc3339)
	if err != nil {
		return 0
	}
	return t.Unix()
}

// Dependency is one entry of a manifest's dependencies: a package version named "Namespace-Name-1.2.3".
type Dependency struct {
	Namespace, Name, Version string
}

// ParseDependency splits "Namespace-Name-1.2.3". The namespace is the text before the first hyphen and the version
// the text after the last; the name between them holds no hyphen, since Thunderstore names use underscores.
func ParseDependency(s string) (Dependency, error) {
	ns, rest, ok := strings.Cut(s, "-")
	name, version, found := strings.CutLast(rest, "-")
	if !ok || !found {
		return Dependency{}, fmt.Errorf("%q is not a Namespace-Name-Version dependency", s)
	}
	d := Dependency{Namespace: ns, Name: name, Version: version}
	if d.Namespace == "" || d.Name == "" || d.Version == "" || strings.Contains(d.Name, "-") {
		return Dependency{}, fmt.Errorf("%q is not a Namespace-Name-Version dependency", s)
	}
	return d, nil
}

// String is the "Namespace-Name-Version" form ParseDependency reads.
func (d Dependency) String() string { return d.Namespace + "-" + d.Name + "-" + d.Version }
