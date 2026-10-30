package thunderstore

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/Rethunk-Tech/go-safefs"
)

// A community's listing is tens of megabytes, and a loader's own package is asked for at every start. The version
// lists of the packages asked for by Versions are kept in a small file beside the listing, stamped with the index
// hash it was read from; a refresh rewrites it from the new listing.

type sideFile struct {
	Hash     string              `json:"hash"`
	Versions map[string][]string `json:"versions"`
}

func (c *Client) sidePath(community string) string {
	return filepath.Join(c.dir(), community+".versions.json")
}

func (c *Client) readSide(community string) sideFile {
	var s sideFile
	if b, err := safefs.ReadFile(c.sidePath(community)); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (c *Client) writeSide(community string, s sideFile) {
	if b, err := json.Marshal(s); err == nil {
		_ = safefs.WriteFileAtomic(c.sidePath(community), b, 0o644)
	}
}

// Versions lists the package's version numbers in the community's index, newest first. The answer comes from a small
// side file when the stored index is the one it was read from, however old that is: the refresh a stale index needs
// runs in the background, not on the caller's path. A miss reads the stored listing a package at a time.
func (c *Client) Versions(ctx context.Context, community, owner, name string) ([]string, error) {
	if out, ok := c.sideVersions(ctx, community, owner, name); ok {
		return out, nil
	}
	p, ok, err := c.Find(ctx, community, owner, name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%s-%s is not in the %s index", owner, name, community)
	}
	out := p.VersionNumbers()
	c.rememberVersions(community, owner, name, out)
	return out, nil
}

func (c *Client) sideVersions(ctx context.Context, community, owner, name string) ([]string, bool) {
	v, ok := c.storedSide(community, owner, name)
	if !ok {
		return nil, false
	}
	if c.now().Sub(c.readMeta(community).Fetched) >= refreshAfter {
		c.refreshInBackground(ctx, community)
	}
	return v, true
}

// storedSide is the side file's list for the package when it was read from the stored index.
func (c *Client) storedSide(community, owner, name string) ([]string, bool) {
	if !communityKey.MatchString(community) {
		return nil, false
	}
	meta := c.readMeta(community)
	if meta.Hash == "" {
		return nil, false
	}
	c.sideMu.Lock()
	s := c.readSide(community)
	c.sideMu.Unlock()
	v, ok := s.Versions[packageID(owner, name)]
	if !ok || s.Hash != meta.Hash || len(v) == 0 {
		return nil, false
	}
	return slices.Clone(v), true
}

// StoredVersions is Versions without the network: the package's version numbers from the side file or the stored
// listing, however old, and false when the community has nothing stored or the package is not in it. It never
// downloads, builds or starts a refresh, and writes nothing.
func (c *Client) StoredVersions(community, owner, name string) ([]string, bool, error) {
	if v, ok := c.storedSide(community, owner, name); ok {
		return v, true, nil
	}
	path, ok := c.storedPath(community)
	if !ok {
		return nil, false, nil
	}
	p, ok, err := findStored(path, owner, name)
	if err != nil || !ok {
		return nil, false, err
	}
	return p.VersionNumbers(), true, nil
}

// rememberVersions adds one package's list, read from the listing the cache holds, to the side file.
func (c *Client) rememberVersions(community, owner, name string, versions []string) {
	meta := c.readMeta(community)
	if meta.Hash == "" {
		return
	}
	c.sideMu.Lock()
	defer c.sideMu.Unlock()
	s := c.readSide(community)
	if s.Hash != meta.Hash || s.Versions == nil {
		s = sideFile{Hash: meta.Hash, Versions: map[string][]string{}}
	}
	s.Versions[packageID(owner, name)] = versions
	c.writeSide(community, s)
}

// refreshSide rewrites the side file for a freshly built listing at path, for the packages it already held. It reads
// the listing a package at a time, so a refresh holds no more of it than those packages.
func (c *Client) refreshSide(community, hash, path string) {
	c.sideMu.Lock()
	old := c.readSide(community)
	c.sideMu.Unlock()
	if len(old.Versions) == 0 || old.Hash == hash {
		return
	}
	found := map[string]Package{}
	err := EachStored(path, func(p *Package) {
		id := p.ID()
		if _, asked := old.Versions[id]; !asked {
			return
		}
		if o, seen := found[id]; !seen || !o.Outranks(*p) {
			found[id] = *p
		}
	})
	if err != nil {
		return
	}
	s := sideFile{Hash: hash, Versions: map[string][]string{}}
	for id, p := range found {
		s.Versions[id] = p.VersionNumbers()
	}
	c.sideMu.Lock()
	defer c.sideMu.Unlock()
	c.writeSide(community, s)
}

// refreshInBackground refreshes the community's index once per refreshAfter, which rewrites the side file when the
// listing changes. A refresh that fails (offline) is not retried until the next window.
func (c *Client) refreshInBackground(ctx context.Context, community string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if last, ok := c.tried[community]; ok && c.fresh(last) {
		return
	}
	c.tried[community] = c.now()
	bg, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
	c.refreshes.Go(func() {
		defer cancel()
		before := c.readMeta(community).Hash
		_, _ = c.buildListing(bg, community)
		if c.cfg.Refreshed != nil && c.readMeta(community).Hash != before {
			c.cfg.Refreshed(community)
		}
	})
}

// Refresh rebuilds the community's stored listing now, without waiting for the refresh window; readers keep
// answering from the stored one until the rebuild lands.
func (c *Client) Refresh(community string) {
	if !communityKey.MatchString(community) {
		return
	}
	if meta := c.readMeta(community); meta.Hash != "" {
		meta.Fetched = time.Time{}
		if nm, err := json.Marshal(meta); err == nil {
			_ = safefs.WriteFileAtomic(c.metaPath(community), nm, 0o644)
		}
	}
	c.mu.Lock()
	delete(c.tried, community)
	c.mu.Unlock()
	c.refreshInBackground(context.Background(), community)
}
