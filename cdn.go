package thunderstore

import (
	"net/url"
	"slices"
)

// CDNs are Thunderstore's download hosts in the order they are tried.
var CDNs = []string{
	"https://ccdn.thunderstore.io",
	"https://gcdn.thunderstore.io",
	"https://hcdn-1.hcdn.thunderstore.io",
}

// OnHost is raw with its scheme and host replaced by those of base, the same path and query.
func OnHost(raw, base string) (string, bool) {
	u, err := url.Parse(raw)
	b, berr := url.Parse(base)
	if err != nil || berr != nil || b.Host == "" {
		return "", false
	}
	u.Scheme, u.Host = b.Scheme, b.Host
	return u.String(), true
}

// CDNOrder is the hosts to try for a download address: only the pinned one when pinned names a host of hosts, else
// the last one that worked first and the rest in order. Nil when the address is not on one of hosts.
func CDNOrder(raw string, hosts []string, pinned, lastGood string) []string {
	u, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	known := slices.ContainsFunc(hosts, func(h string) bool {
		b, err := url.Parse(h)
		return err == nil && b.Host == u.Host
	})
	if !known {
		return nil
	}
	if slices.Contains(hosts, pinned) {
		return []string{pinned}
	}
	if j := slices.Index(hosts, lastGood); j > 0 {
		return slices.Concat([]string{lastGood}, hosts[:j], hosts[j+1:])
	}
	return slices.Clone(hosts)
}
