package thunderstore

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Ref names one package version in a ror2mm link.
type Ref struct {
	Namespace, Name, Version string
}

// Scheme is the one-click install link scheme.
const Scheme = "ror2mm"

var (
	part       = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	versionRe  = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	errBadLink = errors.New("not a ror2mm install link")
)

// ParseLink reads ror2mm://v1/install/thunderstore.io/<namespace>/<name>/<version>/. Namespace and name are
// letters, digits and underscores, the version is three dot-separated numbers; anything else, including a query,
// fragment or userinfo, is refused.
func ParseLink(raw string) (Ref, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != Scheme || u.Host != "v1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return Ref{}, errBadLink
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "install" || parts[1] != "thunderstore.io" ||
		!part.MatchString(parts[2]) || !part.MatchString(parts[3]) || !versionRe.MatchString(parts[4]) {
		return Ref{}, errBadLink
	}
	return Ref{Namespace: parts[2], Name: parts[3], Version: parts[4]}, nil
}
