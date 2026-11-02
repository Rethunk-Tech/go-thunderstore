package thunderstore

import (
	"bytes"
	"context"
	"crypto/md5" // #nosec G501 -- S3's Content-MD5 integrity header
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

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

// Uploader publishes a package zip to Thunderstore the way its CLI does: the zip goes to the site's storage in the
// parts the site asks for, then is submitted under a team in a community.
type Uploader struct {
	client *http.Client
	site   string
	token  string
}

// NewUploader returns an Uploader for the site (such as BaseURL) that authenticates with token. A nil client means
// http.DefaultClient.
func NewUploader(client *http.Client, site, token string) *Uploader {
	if client == nil {
		client = http.DefaultClient
	}
	return &Uploader{client: client, site: strings.TrimRight(site, "/") + "/", token: token}
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
		return &uploadStatusError{prefix: "thunderstore " + path, status: httpkit.StatusError{Code: res.StatusCode, Status: res.Status}, body: strings.TrimSpace(string(reply))}
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
	if part.Offset < 0 || part.Length < 0 || part.Offset+part.Length > int64(len(data)) {
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
		return "", err
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
// URL in that community, or "" when the site names none.
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
	return done.AvailableCommunities[0].URL, nil
}

// storageURL is an address the site handed out to upload bytes to. The file must not cross the internet in the clear:
// only https is followed, or plain http when the API itself was reached that way on the same host.
func storageURL(raw, apiBase string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("thunderstore gave an unusable upload address")
	}
	if api, err := url.Parse(apiBase); u.Scheme != "https" && (err != nil || api.Scheme != "http" || u.Host != api.Host) {
		return "", fmt.Errorf("thunderstore gave an upload address that is not https")
	}
	return raw, nil
}
