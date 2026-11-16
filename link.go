package thunderstore

import (
	"errors"
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
// letters, digits and underscores, the version is three dot-separated numbers; anything else, including a percent
// escape, a query, a fragment or userinfo, is refused.
func ParseLink(raw string) (Ref, error) {
	scheme, rest, ok := strings.Cut(raw, "://")
	if !ok || !strings.EqualFold(scheme, Scheme) {
		return Ref{}, errBadLink
	}
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) != 6 || parts[0] != "v1" || parts[1] != "install" || parts[2] != "thunderstore.io" ||
		!part.MatchString(parts[3]) || !part.MatchString(parts[4]) || !versionRe.MatchString(parts[5]) {
		return Ref{}, errBadLink
	}
	return Ref{Namespace: parts[3], Name: parts[4], Version: parts[5]}, nil
}
