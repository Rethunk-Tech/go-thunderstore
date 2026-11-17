package thunderstore

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- S3's Content-MD5 integrity header
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/Rethunk-Tech/go-httpkit"
	"github.com/Rethunk-Tech/go-safefs"
)

// uploadStatusError is a non-2xx answer from the site or its storage. It unwraps to a *httpkit.StatusError, so
// errors.As finds the status and errors.Is(err, httpkit.ErrNotFound) matches a 404.
type uploadStatusError struct {
	status       httpkit.StatusError
	prefix, body string
}

func (e *uploadStatusError) Error() string {
	if e.body == "" {
		return e.prefix + ": " + e.status.Status
	}
	return e.prefix + ": " + e.status.Status + ": " + e.body
}

func (e *uploadStatusError) Unwrap() error { return &e.status }

var uploadUUID = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

const (
	maxUploadParts = 10000
	maxErrorBody   = 300
)

func cutBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxErrorBody {
		return strings.ToValidUTF8(s[:maxErrorBody], "") + "..."
	}
	return s
}

// checkParts requires the parts to tile the file from offset 0 to its size, in order.
func checkParts(parts []uploadPart, size int64) error {
	if len(parts) > maxUploadParts {
		return fmt.Errorf("thunderstore asked for %d parts", len(parts))
	}
	var next int64
	for _, p := range parts {
		if p.Offset != next || p.Length < 0 {
			return fmt.Errorf("thunderstore part %d does not follow the one before", p.PartNumber)
		}
		if p.Length > size-next {
			return fmt.Errorf("thunderstore part %d runs past the file", p.PartNumber)
		}
		next += p.Length
	}
	if next != size {
		return fmt.Errorf("thunderstore's parts cover %d of %d bytes", next, size)
	}
	return nil
}

// Uploader publishes a package zip to Thunderstore the way its CLI does: the zip goes to the site's storage in the
// parts the site asks for, then is submitted under a team in a community.
type Uploader struct {
	client *http.Client
	site   string
	token  string
	// storageHosts, when set, are the only hosts a part may be sent to.
	storageHosts []string
}

// NewUploader returns an Uploader for the site (such as BaseURL) that authenticates with token. A nil client means
// http.DefaultClient.
func NewUploader(client *http.Client, site, token string) *Uploader {
	if client == nil {
		client = http.DefaultClient
	}
	u := &Uploader{site: strings.TrimRight(site, "/") + "/", token: token}
	u.client = u.withStorageDial(httpkit.WithRedirect(client, u.checkRedirect))
	return u
}

// withStorageDial clones hc (and its *http.Transport) so the dial guard cannot mutate the caller's client or
// http.DefaultTransport. A RoundTripper that is not *http.Transport is left as-is: the guard needs DialContext,
// and replacing the tripper would drop the caller's.
func (u *Uploader) withStorageDial(hc *http.Client) *http.Client {
	c := *hc
	rt := c.Transport
	if rt == nil {
		rt = http.DefaultTransport
	}
	t, ok := rt.(*http.Transport)
	if !ok {
		return &c
	}
	t = t.Clone()
	t.DialContext = u.dialStorage
	c.Transport = &storageTransport{u: u, Transport: t}
	return &c
}

type viaProxyKey struct{}

// storageTransport checks the request URL's resolved IPs when a proxy will be dialed, so Dialer.Control can
// allow the proxy address itself.
type storageTransport struct {
	u *Uploader
	*http.Transport
}

func (s *storageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	proxy, err := proxyFor(s.Transport, req)
	if err != nil {
		return nil, err
	}
	if proxy != nil {
		if err := s.u.checkResolvedHost(req.Context(), req.URL.Hostname()); err != nil {
			return nil, err
		}
		req = req.Clone(context.WithValue(req.Context(), viaProxyKey{}, true))
	}
	return s.Transport.RoundTrip(req)
}

func proxyFor(t *http.Transport, req *http.Request) (*url.URL, error) {
	fn := t.Proxy
	if fn == nil {
		fn = http.ProxyFromEnvironment
	}
	return fn(req)
}

func (u *Uploader) checkResolvedHost(ctx context.Context, host string) error {
	if u.allowPrivate(host) {
		return nil
	}
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if ip, err := netip.ParseAddr(name); err == nil {
		return refuseBlockedIP(ip.Unmap())
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("thunderstore gave an upload address that is not a public host")
	}
	for _, ip := range ips {
		if err := refuseBlockedIP(ip.Unmap()); err != nil {
			return err
		}
	}
	return nil
}

// dialStorage refuses a hop whose resolved address is loopback, link-local, unspecified, or private unless the
// request host is the site itself or on AllowStorageHosts. checkStorage only sees the name, so a DNS record pointing
// at 127.0.0.1 would otherwise pass.
func (u *Uploader) dialStorage(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = hostOnly(addr)
	}
	d := net.Dialer{
		Control: func(_, address string, _ syscall.RawConn) error {
			if ctx.Value(viaProxyKey{}) != nil {
				return nil
			}
			if u.allowPrivate(host) {
				return nil
			}
			return refuseBlockedAddr(address)
		},
	}
	return d.DialContext(ctx, network, addr)
}

func (u *Uploader) allowPrivate(host string) bool {
	name := strings.ToLower(strings.TrimSuffix(host, "."))
	if site, err := url.Parse(u.site); err == nil && site.Host != "" {
		if strings.ToLower(strings.TrimSuffix(hostOnly(site.Host), ".")) == name {
			return true
		}
	}
	return slices.ContainsFunc(u.storageHosts, func(h string) bool {
		return strings.EqualFold(h, name)
	})
}

func refuseBlockedAddr(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = hostOnly(address)
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return fmt.Errorf("thunderstore gave an upload address that is not a public host")
	}
	return refuseBlockedIP(ip.Unmap())
}

var (
	// IsPrivate does not include CGNAT/tailnet or the well-known NAT64 prefix.
	cgnat = netip.MustParsePrefix("100.64.0.0/10")
	nat64 = netip.MustParsePrefix("64:ff9b::/96")
)

func refuseBlockedIP(ip netip.Addr) error {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() || ip.IsUnspecified() || cgnat.Contains(ip) || nat64.Contains(ip) {
		return fmt.Errorf("thunderstore gave an upload address that is not a public host")
	}
	return nil
}

// checkRedirect keeps every hop on the host the first request named, so a storage endpoint cannot 307/308 the zip
// (Go keeps PUT and the body on those) to a host checkStorage never accepted.
func (u *Uploader) checkRedirect(req *http.Request, via []*http.Request) error {
	if err := httpkit.NoDowngrade(req, via); err != nil {
		return err
	}
	if len(via) > 0 && req.URL.Host != via[0].URL.Host {
		return fmt.Errorf("thunderstore: upload redirect to %s refused", req.URL.Host)
	}
	return nil
}

// AllowStorageHosts restricts where the zip may be sent to the named hostnames (no port). A listed name may resolve
// to a private or loopback address; without this call those are refused at dial time. Thunderstore deployments
// choose their storage endpoint themselves (USERMEDIA_S3_ENDPOINT_URL in its settings), so there is no host the library
// can assume; without this call the parts must still share one non-IP host, which the site's own answer names.
func (u *Uploader) AllowStorageHosts(hosts ...string) { u.storageHosts = hosts }

// checkStorage requires every part to go to one host (a deployment has one storage endpoint, so a reply that splits the
// file across hosts is not one), that host to be a name rather than an IP address unless it is the site itself, and it
// to be on the AllowStorageHosts list when there is one.
func (u *Uploader) checkStorage(parts []uploadPart) error {
	site, _ := url.Parse(u.site)
	var host string
	for _, p := range parts {
		pu, err := url.Parse(p.URL)
		if err != nil || pu.Host == "" {
			return fmt.Errorf("thunderstore gave an unusable upload address")
		}
		if host == "" {
			host = pu.Host
		}
		if pu.Host != host {
			return fmt.Errorf("thunderstore split the upload across hosts")
		}
	}
	if host == "" || (site != nil && host == site.Host) {
		return nil
	}
	name := strings.ToLower(strings.TrimSuffix(hostOnly(host), "."))
	// A name that is not ASCII is refused rather than resolved: IDNA maps fullwidth digits and lookalikes onto
	// ASCII, so １２７.０.０.１ is loopback the checks below never see.
	if nonASCII(name) {
		return fmt.Errorf("thunderstore gave an upload host that is not an ASCII name")
	}
	if _, err := netip.ParseAddr(name); err == nil || atonIPv4(name) {
		return fmt.Errorf("thunderstore gave an upload address that is an IP address")
	}
	if len(u.storageHosts) > 0 {
		if !slices.ContainsFunc(u.storageHosts, func(h string) bool { return strings.EqualFold(h, name) }) {
			return fmt.Errorf("thunderstore gave an upload host that is not allowed: %s", name)
		}
		return nil
	}
	if name == "localhost" || strings.HasSuffix(name, ".localhost") {
		return fmt.Errorf("thunderstore gave an upload address that is the local machine")
	}
	return nil
}

// nonASCII reports whether s holds a byte outside ASCII.
func nonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

// atonIPv4 reports the IPv4 forms Unix getaddrinfo treats as addresses (hex, octal, and a.b / a.b.c) that
// netip.ParseAddr does not, so a part URL of https://0x7f000001/ is refused like 127.0.0.1.
func atonIPv4(s string) bool {
	parts := strings.Split(s, ".")
	if s == "" || len(parts) > 4 {
		return false
	}
	var vals [4]uint64
	for i, p := range parts {
		if p == "" {
			return false
		}
		base, digits := 10, p
		switch {
		case strings.HasPrefix(p, "0x"):
			if len(p) == 2 {
				return false
			}
			base, digits = 16, p[2:]
		case len(p) > 1 && p[0] == '0':
			base = 8
		}
		v, err := strconv.ParseUint(digits, base, 32)
		if err != nil {
			return false
		}
		vals[i] = v
	}
	switch len(parts) {
	case 1:
		return vals[0] <= 0xffffffff
	case 2:
		return vals[0] <= 0xff && vals[1] <= 0xffffff
	case 3:
		return vals[0] <= 0xff && vals[1] <= 0xff && vals[2] <= 0xffff
	default:
		return vals[0] <= 0xff && vals[1] <= 0xff && vals[2] <= 0xff && vals[3] <= 0xff
	}
}

type uploadPart struct {
	PartNumber int    `json:"part_number"`
	URL        string `json:"url"`
	Offset     int64  `json:"offset"`
	Length     int64  `json:"length"`
}

type finishedPart struct {
	ETag       string `json:"ETag"`
	PartNumber int    `json:"PartNumber"`
}

func (u *Uploader) call(ctx context.Context, path string, body, out any) error {
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.site+"api/experimental/"+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+u.token)
	res, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = res.Body.Close() }()
	reply, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= http.StatusBadRequest {
		return &uploadStatusError{prefix: "thunderstore " + path, status: httpkit.StatusError{Code: res.StatusCode, Status: res.Status}, body: cutBody(string(reply))}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(reply, out)
}

// Upload sends the zip at zipPath to Thunderstore's storage and returns the upload's UUID, to pass to Submit. A failed
// part aborts the upload.
func (u *Uploader) Upload(ctx context.Context, zipPath string) (string, error) {
	data, err := safefs.ReadFile(zipPath)
	if err != nil {
		return "", err
	}
	var started struct {
		UserMedia struct {
			UUID string `json:"uuid"`
		} `json:"user_media"`
		UploadURLs []uploadPart `json:"upload_urls"`
	}
	if err := u.call(ctx, "usermedia/initiate-upload/", map[string]any{"filename": filepath.Base(zipPath), "file_size_bytes": len(data)}, &started); err != nil {
		return "", err
	}
	uuid := started.UserMedia.UUID
	if !uploadUUID.MatchString(uuid) {
		return "", fmt.Errorf("thunderstore returned an invalid upload id")
	}
	if err := checkParts(started.UploadURLs, int64(len(data))); err != nil {
		_ = u.call(context.WithoutCancel(ctx), "usermedia/"+uuid+"/abort-upload/", map[string]any{}, nil)
		return "", err
	}
	if err := u.checkStorage(started.UploadURLs); err != nil {
		_ = u.call(context.WithoutCancel(ctx), "usermedia/"+uuid+"/abort-upload/", map[string]any{}, nil)
		return "", err
	}
	parts := make([]finishedPart, 0, len(started.UploadURLs))
	for _, part := range started.UploadURLs {
		etag, err := u.putPart(ctx, data, part)
		if err != nil {
			_ = u.call(context.WithoutCancel(ctx), "usermedia/"+uuid+"/abort-upload/", map[string]any{}, nil)
			return "", err
		}
		parts = append(parts, finishedPart{ETag: etag, PartNumber: part.PartNumber})
	}
	return uuid, u.call(ctx, "usermedia/"+uuid+"/finish-upload/", map[string]any{"parts": parts}, nil)
}

func (u *Uploader) putPart(ctx context.Context, data []byte, part uploadPart) (string, error) {
	if part.Offset < 0 || part.Length < 0 || part.Offset > int64(len(data)) || part.Length > int64(len(data))-part.Offset {
		return "", fmt.Errorf("thunderstore asked for part %d outside the file", part.PartNumber)
	}
	chunk := data[part.Offset : part.Offset+part.Length]
	sum := md5.Sum(chunk) // #nosec G401 -- S3's Content-MD5 integrity header
	target, err := storageURL(part.URL, u.site)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, target, bytes.NewReader(chunk))
	if err != nil {
		return "", err
	}
	req.ContentLength = part.Length
	req.Header.Set("Content-MD5", base64.StdEncoding.EncodeToString(sum[:]))
	res, err := u.client.Do(req)
	if err != nil {
		// *url.Error carries the presigned URL, whose query is a credential.
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return "", fmt.Errorf("uploading part %d: %w", part.PartNumber, err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= http.StatusBadRequest {
		return "", &uploadStatusError{prefix: fmt.Sprintf("uploading part %d", part.PartNumber), status: httpkit.StatusError{Code: res.StatusCode, Status: res.Status}}
	}
	etag := res.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("uploading part %d: no ETag", part.PartNumber)
	}
	return etag, nil
}

// Submit publishes the upload uuid under team in community, filed under categories, and returns the package's page
// URL in that community, or "" when the site names none or names one that is not an http(s) address.
func (u *Uploader) Submit(ctx context.Context, uuid, team, community string, categories []string) (string, error) {
	var done struct {
		AvailableCommunities []struct {
			URL string `json:"url"`
		} `json:"available_communities"`
	}
	body := map[string]any{
		"upload_uuid":          uuid,
		"author_name":          team,
		"communities":          []string{community},
		"categories":           categories,
		"community_categories": map[string][]string{community: categories},
		"has_nsfw_content":     false,
	}
	if err := u.call(ctx, "submission/submit/", body, &done); err != nil {
		return "", err
	}
	if len(done.AvailableCommunities) == 0 {
		return "", nil
	}
	return webLink(done.AvailableCommunities[0].URL), nil
}

// storageURL is an address the site handed out to upload bytes to. The file must not cross the internet in the clear:
// only https is followed, or plain http when the API itself was reached that way on the same host.
func storageURL(raw, apiBase string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("thunderstore gave an unusable upload address")
	}
	if u.Scheme == "https" {
		return raw, nil
	}
	api, aerr := url.Parse(apiBase)
	if aerr != nil || u.Scheme != "http" || api.Scheme != "http" || u.Host != api.Host {
		return "", fmt.Errorf("thunderstore gave an upload address that is not https")
	}
	return raw, nil
}
