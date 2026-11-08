package thunderstore

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"runtime/debug"
	"slices"
	"time"

	"github.com/Rethunk-Tech/go-safefs"
)

// Index is one community's package listing, loaded whole. Dependency names and lists, version numbers, owners,
// categories and repositories are held once however many packages repeat them: a dependency list is a varint run of
// names-table indexes, about half the bytes of the same indexes as uint32s.
type Index struct{ pk []Package }

// Packages is every package, one per case-folded id. The slice and what it holds are shared: do not modify them.
func (x *Index) Packages() []Package { return x.pk }

// Each calls fn with each package in listing order.
func (x *Index) Each(fn func(*Package)) {
	for i := range x.pk {
		fn(&x.pk[i])
	}
}

// Find is the package owner-name, compared case-insensitively.
func (x *Index) Find(owner, name string) (Package, bool) {
	id := packageID(owner, name)
	i := slices.IndexFunc(x.pk, func(p Package) bool { return p.ID() == id })
	if i < 0 {
		return Package{}, false
	}
	return x.pk[i], true
}

// depTable holds each distinct dependency string of a listing once; versions refer to them by index.
type depTable struct{ names []string }

// depNames numbers each distinct dependency string as a listing is built.
type depNames struct {
	names []string
	index map[string]uint32
}

// ids is the dependency list of one version as Version holds it, numbering each name once.
func (n *depNames) ids(deps []string) string {
	if len(deps) == 0 {
		return ""
	}
	out := make([]uint32, len(deps))
	for i, d := range deps {
		id, ok := n.index[d]
		if !ok {
			id = uint32(len(n.names) & math.MaxUint32)
			n.names = append(n.names, d)
			n.index[d] = id
		}
		out[i] = id
	}
	return encodeIDs(out)
}

// depCompactor holds once what repeats across a loaded listing's packages: identical dependency lists, category
// lists, version numbers, owners, categories and repositories.
type depCompactor struct {
	tab   *depTable
	lists map[string]string
	cats  map[string][]string
	words map[string]string
	key   []byte
}

func newDepCompactor() *depCompactor {
	return &depCompactor{tab: &depTable{}, lists: map[string]string{}, cats: map[string][]string{}, words: map[string]string{}}
}

// word returns the one copy of s held for the listing.
func (c *depCompactor) word(s string) string {
	if w, ok := c.words[s]; ok {
		return w
	}
	c.words[s] = s
	return s
}

func (c *depCompactor) add(p *Package) {
	p.tab = c.tab
	// The decoder grows slices by doubling; the listing keeps them at their length.
	p.Versions = append(make([]Version, 0, len(p.Versions)), p.Versions...)
	p.Categories = c.categories(p.Categories)
	p.Owner, p.Repo = c.word(p.Owner), c.word(p.Repo)
	for j := range p.Versions {
		v := &p.Versions[j]
		v.Number = c.word(v.Number)
		if v.deps == "" {
			continue
		}
		// The map's key and the version's field are the one string, so a distinct list keeps a single copy.
		shared, ok := c.lists[v.deps]
		if !ok {
			c.lists[v.deps] = v.deps
			continue
		}
		v.deps = shared
	}
}

// categories is the one copy of a category list held for the listing; a listing repeats the same few lists.
func (c *depCompactor) categories(cats []string) []string {
	c.key = c.key[:0]
	for _, cat := range cats {
		c.key = binary.AppendUvarint(c.key, uint64(len(cat)))
		c.key = append(c.key, cat...)
	}
	if shared, ok := c.cats[string(c.key)]; ok {
		return shared
	}
	cats = append(make([]string, 0, len(cats)), cats...)
	for i, cat := range cats {
		cats[i] = c.word(cat)
	}
	c.cats[string(c.key)] = cats
	return cats
}

// known reports whether every dependency index a version holds has a name.
func (c *depCompactor) known() bool {
	ok := true
	for deps := range c.lists {
		eachID(deps, func(id uint32) { ok = ok && int(id) < len(c.tab.names) })
	}
	return ok
}

// encodeIDs is deps as the varints Version holds, each index one varint.
func encodeIDs(deps []uint32) string {
	if len(deps) == 0 {
		return ""
	}
	out := make([]byte, 0, len(deps)*2)
	for _, id := range deps {
		out = binary.AppendUvarint(out, uint64(id))
	}
	return string(out)
}

// eachID calls fn with each names-table index enc holds, in order, reading the string in place.
func eachID(enc string, fn func(uint32)) {
	var id uint32
	var shift uint
	for i := 0; i < len(enc); i++ {
		b := enc[i]
		id |= uint32(b&0x7f) << shift
		if b < 0x80 {
			fn(id)
			id, shift = 0, 0
			continue
		}
		shift += 7
	}
}

// decodeIDs is the indexes enc holds; nil when it holds none.
func decodeIDs(enc string) []uint32 {
	if enc == "" {
		return nil
	}
	out := make([]uint32, 0, len(enc)/2+1)
	eachID(enc, func(id uint32) { out = append(out, id) })
	return out
}

func expectArray(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok != json.Delim('[') {
		return errors.New("package list is not a JSON array")
	}
	return nil
}

// readPackages decodes the stored listing at path.
func readPackages(path string) ([]Package, error) {
	f, err := safefs.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(bufio.NewReader(f))
	if err := expectArray(dec); err != nil {
		return nil, err
	}
	var pk []Package
	c := newDepCompactor()
	for dec.More() {
		var p Package
		if err := dec.Decode(&p); err != nil {
			return nil, err
		}
		c.add(&p)
		pk = append(pk, p)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if err := dec.Decode(&c.tab.names); err != nil {
		return nil, err
	}
	if !c.known() {
		return nil, errors.New("the listing names a dependency it does not hold")
	}
	// append's doubling leaves a backing array up to twice the listing's length, and the collector keeps the whole
	// allocation for as long as the memo holds the slice: a large community's packages are hundreds of bytes each, so
	// the spare tail is megabytes.
	return slices.Clone(dedupeIDs(pk)), nil
}

// EachStored hands fn each package of the stored listing at path in turn, keeping none of them; Dependencies of
// those packages is empty. A package the site listed under two case-folded ids is handed twice: Package.Outranks
// picks.
func EachStored(path string, fn func(*Package)) error {
	f, err := safefs.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	dec := json.NewDecoder(bufio.NewReader(f))
	if err := expectArray(dec); err != nil {
		return err
	}
	for dec.More() {
		var p Package
		if err := dec.Decode(&p); err != nil {
			return err
		}
		fn(&p)
	}
	return nil
}

// dedupeIDs keeps one listing per case-folded id. Thunderstore treats names case-insensitively but lets a deprecated
// package keep a differently-cased twin of a live one, so every lookup by id would otherwise land on whichever came
// last.
func dedupeIDs(pk []Package) []Package {
	at := make(map[string]int, len(pk))
	drop := false
	for i, p := range pk {
		id := p.ID()
		j, seen := at[id]
		if !seen {
			at[id] = i
			continue
		}
		drop = true
		if pk[j].Outranks(p) {
			pk[i].Versions = nil
		} else {
			pk[j].Versions = nil
			at[id] = i
		}
	}
	if !drop {
		return pk
	}
	return slices.DeleteFunc(pk, func(p Package) bool { return p.Versions == nil })
}

type memoEntry struct {
	path string
	idx  *Index
	// idle drops the entry once nothing has asked for the listing for listingIdle.
	idle *time.Timer
}

// load is the community's index read from path, kept in memory until nothing has asked for it for listingIdle.
func (c *Client) load(community, path string) (*Index, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m, ok := c.memo[community]; ok && m.path == path {
		m.idle.Reset(c.listingIdle)
		return m.idx, nil
	}
	pk, err := readPackages(path)
	if err != nil {
		return nil, err
	}
	if old, ok := c.memo[community]; ok {
		old.idle.Stop()
	}
	idx := &Index{pk}
	c.memo[community] = memoEntry{path, idx, time.AfterFunc(c.listingIdle, func() { c.release(community, path) })}
	return idx, nil
}

// memoized is the index of community read from path when it is already in memory, without loading it.
func (c *Client) memoized(community, path string) (*Index, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.memo[community]
	return m.idx, ok && m.path == path
}

// release drops the loaded index of community when it is still the one read from path.
func (c *Client) release(community, path string) {
	c.mu.Lock()
	m, ok := c.memo[community]
	ok = ok && m.path == path
	if ok {
		m.idle.Stop()
		delete(c.memo, community)
	}
	c.mu.Unlock()
	if ok {
		// Nothing allocates enough to collect the dropped listing soon, and its pages stay with the process until the
		// runtime's slow scavenger returns them.
		debug.FreeOSMemory()
	}
}
