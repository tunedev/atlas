package crawlsource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
)

// revalidator keeps, per URL, the last full body and the validators that
// came with it, so the next request for that URL can be conditional.
type revalidator struct {
	dir string
}

// stored is one URL's cached response: its validators, its Content-Type and
// its body.
type stored struct {
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
	ContentType  string `json:"content_type"`
	Body         []byte `json:"body"`
}

func (v revalidator) path(u string) string {
	sum := sha256.Sum256([]byte(u))
	return filepath.Join(v.dir, hex.EncodeToString(sum[:])+".json")
}

// load returns u's cached response, or false when there is none or it
// cannot be read.
func (v revalidator) load(u string) (stored, bool) {
	raw, err := os.ReadFile(v.path(u))
	if err != nil {
		return stored{}, false
	}
	var s stored
	if json.Unmarshal(raw, &s) != nil {
		return stored{}, false
	}
	return s, true
}

// condition makes req conditional on s's validators.
func (v revalidator) condition(req *http.Request, s stored) {
	if s.ETag != "" {
		req.Header.Set("If-None-Match", s.ETag)
	}
	if s.LastModified != "" {
		req.Header.Set("If-Modified-Since", s.LastModified)
	}
}

// save keeps a 200 response's body when it came with a validator.
func (v revalidator) save(u string, resp *http.Response, body []byte) error {
	s := stored{ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), ContentType: resp.Header.Get("Content-Type"), Body: body}
	if s.ETag == "" && s.LastModified == "" {
		return nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(v.dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(v.path(u), raw, 0o600)
}

// revalidationsKey is the context key of a revalidation counter.
type revalidationsKey struct{}

// countingRevalidations returns ctx carrying a fresh counter of the requests
// made under it that were answered "not modified" and served from the cache.
func countingRevalidations(ctx context.Context) (context.Context, *atomic.Int64) {
	n := new(atomic.Int64)
	return context.WithValue(ctx, revalidationsKey{}, n), n
}

// countRevalidation adds one to ctx's revalidation counter, if it carries one.
func countRevalidation(ctx context.Context) {
	if n, ok := ctx.Value(revalidationsKey{}).(*atomic.Int64); ok {
		n.Add(1)
	}
}
