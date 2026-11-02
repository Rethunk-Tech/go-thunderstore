package thunderstore

import (
	"bufio"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-safefs"
)

const (
	// BaseURL is the real site.
	BaseURL = "https://thunderstore.io"

	// refreshAfter is how long a stored index is used without asking the site whether it changed.
	refreshAfter = time.Hour
	// refreshTimeout bounds a background refresh, which downloads every chunk of a large community.
	refreshTimeout = 5 * time.Minute
	requestTimeout = 20 * time.Second
	// maxBody caps one HTTP answer; maxUnpacked caps what a gzipped answer may unpack to.
	maxBody     = 16 << 20
	maxUnpacked = maxBody * 4
	maxChunks   = 500

	// listingTag names the shape of a stored listing. It changes when the shape does, so a listing built under another
	// shape is never read.
	listingTag = "c4"
)

var communityKey = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Config is what a Client is built from.
type Config struct {
	// CacheDir is required. The stored listings live in its thunderstore folder.
	CacheDir string
	// HTTP sends every request; nil means http.DefaultClient. Redirects from https to another scheme are refused.
	HTTP *http.Client
	// BaseURL is the site; empty means BaseURL. Chunk addresses the index names must be on this host or on
	// thunderstore.io.
	BaseURL string
	// UserAgent is sent with every request.
	UserAgent string
	// Now is the clock the hourly refresh reads; nil means time.Now.
	Now func() time.Time
	// Refreshed is told the community when a background refresh stored a changed listing. It runs on the refresh's
	// goroutine.
	Refreshed func(community string)
	// Built is told the community and the path of each listing newly stored, before Index sees it; the path stays valid
	// for EachStored until the next build replaces it. It runs on the build's goroutine.
	Built func(community, path string)
}

// Client reads Thunderstore's package indexes through an on-disk cache. A stored listing is answered at once however
// old: past an hour a rebuild runs in the background, so only a community with nothing stored waits on the download.
// Use one Client per cache folder; it is safe for concurrent use.
type Client struct {
	cfg         Config
	hc          *http.Client
	maxUnpacked int64
	listingIdle time.Duration

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	memo  map[string]memoEntry
	tried map[string]time.Time

	sideMu    sync.Mutex
	refreshes sync.WaitGroup
}

// New returns a Client for cfg.
func New(cfg Config) (*Client, error) {
	if cfg.CacheDir == "" {
		return nil, errors.New("thunderstore: Config.CacheDir is required")
	}
	cfg.BaseURL = strings.TrimRight(cmp.Or(cfg.BaseURL, BaseURL), "/")
	return &Client{
		cfg: cfg, hc: httpkit.WithRedirect(cmp.Or(cfg.HTTP, http.DefaultClient), httpkit.NoDowngrade),
		maxUnpacked: maxUnpacked, listingIdle: 10 * time.Minute,
		locks: map[string]*sync.Mutex{}, memo: map[string]memoEntry{}, tried: map[string]time.Time{},
	}, nil
}

// Wait blocks until the background refreshes started so far have finished, so a caller may remove the cache folder.
func (c *Client) Wait() { c.refreshes.Wait() }

// Index is the community's package listing, loaded whole and kept in memory until nothing has asked for it for ten
// minutes. A listing holds tens of megabytes for a large community: prefer Each or Find for one pass or one package.
func (c *Client) Index(ctx context.Context, community string) (*Index, error) {
	path, err := c.ListingFile(ctx, community)
	if err != nil {
		return nil, err
	}
	return c.load(community, path)
}

// Snapshot is the community's index for one pass of a check that runs often: the copy Index holds when it holds one,
// else read from disk and left to the collector.
func (c *Client) Snapshot(ctx context.Context, community string) (*Index, error) {
	path, err := c.ListingFile(ctx, community)
	if err != nil {
		return nil, err
	}
	if x, ok := c.memoized(community, path); ok {
		return x, nil
	}
	pk, err := readPackages(path)
	if err != nil {
		return nil, err
	}
	return &Index{pk}, nil
}

// Each calls fn with each package of the community's listing, from memory when Index holds it, else a package at a
// time from disk. Packages read from disk carry no Dependencies, and a package listed under two case-folded ids comes
// twice: Package.Outranks picks.
func (c *Client) Each(ctx context.Context, community string, fn func(*Package)) error {
	path, err := c.ListingFile(ctx, community)
	if err != nil {
		return err
	}
	if x, ok := c.memoized(community, path); ok {
		x.Each(fn)
		return nil
	}
	return EachStored(path, fn)
}

// Find is the package owner-name in the community's listing, read a package at a time without loading the listing;
// ok is false when it is not listed. The package carries no Dependencies.
func (c *Client) Find(ctx context.Context, community, owner, name string) (p Package, ok bool, err error) {
	path, err := c.ListingFile(ctx, community)
	if err != nil {
		return Package{}, false, err
	}
	return findStored(path, owner, name)
}

func findStored(path, owner, name string) (found Package, ok bool, err error) {
	id := packageID(owner, name)
	err = EachStored(path, func(p *Package) {
		if p.ID() == id && (!ok || !found.Outranks(*p)) {
			found, ok = *p, true
		}
	})
	return found, ok, err
}

// ListingFile is the path of the community's stored listing, built first when the community has none. The file is a
// private format: read it with EachStored.
func (c *Client) ListingFile(ctx context.Context, community string) (string, error) {
	if !communityKey.MatchString(community) {
		return "", fmt.Errorf("%q is not a Thunderstore community key", community)
	}
	dir := c.dir()
	meta := c.readMeta(community)
	path := listingPath(dir, community, meta.Hash)
	if _, err := os.Stat(path); meta.Hash != "" && err == nil {
		if !c.fresh(meta.Fetched) {
			c.refreshInBackground(ctx, community)
		}
		return path, nil
	}
	return c.buildListing(ctx, community)
}

// Stored calls fn with each package of the community's listing without touching the network: from memory when Index
// holds it, else a package at a time from the stored file, however old it is. It reports false, having called nothing,
// when the community has no listing stored. It never downloads, builds or starts a refresh. Packages from memory carry
// Dependencies; those read from the file do not, and a case-folded twin may come twice, as with Each.
func (c *Client) Stored(_ context.Context, community string, fn func(*Package)) (bool, error) {
	if !communityKey.MatchString(community) {
		return false, fmt.Errorf("%q is not a Thunderstore community key", community)
	}
	c.mu.Lock()
	m, held := c.memo[community]
	c.mu.Unlock()
	if held {
		m.idx.Each(fn)
		return true, nil
	}
	path, ok := c.storedPath(community)
	if !ok {
		return false, nil
	}
	return true, EachStored(path, fn)
}

// Held reports whether Index holds the community's listing in memory.
func (c *Client) Held(community string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.memo[community]
	return ok
}

// storedPath is the community's stored listing file, if there is one.
func (c *Client) storedPath(community string) (string, bool) {
	if !communityKey.MatchString(community) {
		return "", false
	}
	meta := c.readMeta(community)
	path := listingPath(c.dir(), community, meta.Hash)
	_, err := os.Stat(path)
	return path, meta.Hash != "" && err == nil
}

// cacheMeta records which index blob the stored listing was built from.
type cacheMeta struct {
	Hash    string    `json:"hash"`
	Fetched time.Time `json:"fetched"`
}

func (c *Client) dir() string { return filepath.Join(c.cfg.CacheDir, "thunderstore") }

func (c *Client) metaPath(community string) string {
	return filepath.Join(c.dir(), community+".meta.json")
}

func (c *Client) readMeta(community string) (m cacheMeta) {
	if b, err := safefs.ReadFile(c.metaPath(community)); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func (c *Client) now() time.Time {
	if c.cfg.Now != nil {
		return c.cfg.Now()
	}
	return time.Now()
}

// fresh reports whether a listing fetched at t is younger than refreshAfter; one stamped in the future (a clock set
// back) is not.
func (c *Client) fresh(t time.Time) bool {
	age := c.now().Sub(t)
	return age >= 0 && age < refreshAfter
}

func (c *Client) buildLock(community string) *sync.Mutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.locks[community] == nil {
		c.locks[community] = &sync.Mutex{}
	}
	return c.locks[community]
}

// chunkAllowed reports whether the index may point the client at raw: Thunderstore's own https hosts, or the host the
// index itself came from.
func (c *Client) chunkAllowed(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if b, err := url.Parse(c.cfg.BaseURL); err == nil && u.Scheme == b.Scheme && u.Host == b.Host {
		return true
	}
	host := u.Hostname()
	return u.Scheme == "https" && (host == "thunderstore.io" || strings.HasSuffix(host, ".thunderstore.io"))
}

// request builds a GET for url that gives up after requestTimeout.
func (c *Client) request(ctx context.Context, url string) (*http.Request, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	return req, cancel, nil
}

// open starts a GET and returns its body, which the caller reads before calling done.
func (c *Client) open(ctx context.Context, url string) (body io.Reader, done func(), err error) {
	req, cancel, err := c.request(ctx, url)
	if err != nil {
		return nil, nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	done = func() { _ = resp.Body.Close(); cancel() }
	if resp.StatusCode != http.StatusOK {
		done()
		return nil, nil, &httpkit.StatusError{Code: resp.StatusCode, Status: resp.Status}
	}
	return safefs.CapReader(resp.Body, maxBody), done, nil
}

func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	req, cancel, err := c.request(ctx, url)
	if err != nil {
		return nil, err
	}
	defer cancel()
	return httpkit.DoCapped(c.hc, req, maxBody, nil)
}

// streamPackages decodes a package-list chunk one package at a time, so only the package being read is held.
func (c *Client) streamPackages(ctx context.Context, url string, fn func(*wirePackage) error) error {
	body, done, err := c.open(ctx, url)
	if err != nil {
		return err
	}
	defer done()
	br := bufio.NewReader(body)
	var r io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		r = safefs.CapReader(zr, c.maxUnpacked)
	}
	dec := json.NewDecoder(r)
	if err := expectArray(dec); err != nil {
		return err
	}
	var w wirePackage
	for dec.More() {
		w = wirePackage{}
		if err := dec.Decode(&w); err != nil {
			return err
		}
		if err := fn(&w); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}

// getJSON fetches url and decodes it, gunzipping a body that is still compressed.
func (c *Client) getJSON(ctx context.Context, url string, out any) (hash string, err error) {
	raw, err := c.get(ctx, url)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	body := raw
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return "", err
		}
		if body, err = safefs.ReadCapped(zr, c.maxUnpacked); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(sum[:]), json.Unmarshal(body, out)
}

func (c *Client) indexURL(community string) string {
	return c.cfg.BaseURL + "/c/" + community + "/api/v1/package-listing-index/"
}

func listingPath(dir, community, hash string) string {
	return filepath.Join(dir, community+"-"+listingTag+"-"+hash+".json")
}

// dropOtherListings removes every stored listing of the community but keep: an earlier index hash's, one built
// under an earlier tag, which nothing would read again, and a build's interrupted temp file. A community whose key
// merely starts with this one is left.
func dropOtherListings(dir, community, keep string) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		rest, ok := strings.CutPrefix(e.Name(), community+"-c")
		tag, _, tagged := strings.Cut(rest, "-")
		path := filepath.Join(dir, e.Name())
		if !ok || !tagged || tag == "" || strings.Trim(tag, "0123456789") != "" || !listingOrTemp(rest) || path == keep {
			continue
		}
		_ = safefs.Remove(path)
	}
}

// listingOrTemp reports a listing file or a build's interrupted temp file, "<listing>.json.<n>.tmp"
// (safefs.WriteStreamAtomic).
func listingOrTemp(rest string) bool {
	return strings.HasSuffix(rest, ".json") || strings.HasSuffix(rest, ".tmp") && strings.Contains(rest, ".json.")
}

// buildListing is the community's stored listing, downloaded and built first when the stored one is older than
// refreshAfter, kept when the site cannot be reached.
func (c *Client) buildListing(ctx context.Context, community string) (string, error) {
	// One build per community: a second caller waits, then finds the listing the first one wrote.
	mu := c.buildLock(community)
	mu.Lock()
	defer mu.Unlock()
	dir := c.dir()
	meta := c.readMeta(community)
	cached := func() (string, bool) {
		path := listingPath(dir, community, meta.Hash)
		_, err := os.Stat(path)
		return path, meta.Hash != "" && err == nil
	}
	if path, ok := cached(); ok && c.fresh(meta.Fetched) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	var chunks []string
	hash, err := c.getJSON(ctx, c.indexURL(community), &chunks)
	if err != nil {
		if path, ok := cached(); ok {
			return path, nil
		}
		return "", err
	}
	if len(chunks) > maxChunks {
		return "", fmt.Errorf("index lists %d chunks", len(chunks))
	}
	for _, u := range chunks {
		if !c.chunkAllowed(u) {
			return "", fmt.Errorf("index names a chunk outside Thunderstore: %s", u)
		}
	}
	path := listingPath(dir, community, hash)
	if _, err := os.Stat(path); err != nil {
		if err := c.build(ctx, chunks, path); err != nil {
			return "", err
		}
	}
	dropOtherListings(dir, community, path)
	nm, err := json.Marshal(cacheMeta{Hash: hash, Fetched: c.now()})
	if err != nil {
		return "", err
	}
	if err := safefs.WriteFileAtomic(c.metaPath(community), nm, 0o644); err != nil {
		return "", err
	}
	c.refreshSide(community, hash, path)
	if c.cfg.Built != nil {
		c.cfg.Built(community, path)
	}
	return path, nil
}

// build downloads every chunk and writes the slimmed listing to path as it goes, so neither a chunk nor the whole
// listing is held in memory. The file is two JSON values: the packages, then the dependency names their versions
// index, each name once (the same "Owner-Name-1.2.3" pin is named by most versions of most packages).
// A changed index rebuilds the listing from every chunk (~35 MB downloaded, ~63 MB rewritten per refresh): chunks are
// content-addressed, so none survive an index change. The lever is refreshAfter.
func (c *Client) build(ctx context.Context, chunks []string, path string) error {
	return safefs.WriteStreamAtomic(path, 0o644, func(w io.Writer) error {
		bw := bufio.NewWriter(w)
		enc := json.NewEncoder(bw)
		if err := bw.WriteByte('['); err != nil {
			return err
		}
		first := true
		names := depNames{index: map[string]uint32{}}
		for _, u := range chunks {
			err := c.streamPackages(ctx, u, func(w *wirePackage) error {
				if len(w.Versions) == 0 {
					return nil
				}
				if !first {
					if err := bw.WriteByte(','); err != nil {
						return err
					}
				}
				first = false
				return enc.Encode(slim(w, &names))
			})
			if err != nil {
				return err
			}
		}
		if err := bw.WriteByte(']'); err != nil {
			return err
		}
		if err := enc.Encode(names.names); err != nil {
			return err
		}
		return bw.Flush()
	})
}

// Category is one of a community's package categories. Slug is what Submit files a package under.
type Category struct {
	Slug, Name string
}

// Categories lists the community's package categories. A failed request is a *httpkit.StatusError, as for the index.
func (c *Client) Categories(ctx context.Context, community string) ([]Category, error) {
	if !communityKey.MatchString(community) {
		return nil, fmt.Errorf("%q is not a Thunderstore community key", community)
	}
	var filters struct {
		Categories []struct {
			Slug string `json:"slug"`
			Name string `json:"name"`
		} `json:"package_categories"`
	}
	if _, err := c.getJSON(ctx, c.cfg.BaseURL+"/api/cyberstorm/community/"+community+"/filters/", &filters); err != nil {
		return nil, err
	}
	out := make([]Category, len(filters.Categories))
	for i, cat := range filters.Categories {
		out[i] = Category{Slug: cat.Slug, Name: cat.Name}
	}
	return out, nil
}
