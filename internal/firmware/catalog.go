package firmware

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// indexTTL is how long a fetched index is served before it's fetched
	// again. New builds take about this long to show up.
	indexTTL = 5 * time.Minute
	// retryAfterError is how soon a failed refresh is tried again while the
	// previous index is still being served.
	retryAfterError = time.Minute
	// fetchTimeout bounds one fetch of index.json.
	fetchTimeout = 10 * time.Second
	// maxIndexBytes caps index.json. It lists each board's published builds,
	// a few hundred bytes apiece, so this is far beyond any real catalog.
	maxIndexBytes = 4 << 20
)

// Catalog fetches the firmware host's index.json and caches it. It is safe for
// concurrent use; concurrent callers share one fetch.
type Catalog struct {
	base     *url.URL
	indexURL string
	client   *http.Client
	now      func() time.Time

	mu        sync.Mutex
	cached    *Index
	refreshAt time.Time
}

// NewCatalog returns a catalog for the firmware host at baseURL (an absolute
// http or https URL; index.json is read from its root path).
func NewCatalog(baseURL string) (*Catalog, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("firmware URL: %w", err)
	}
	if (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("firmware URL %q must be an absolute http(s) URL", baseURL)
	}
	return &Catalog{
		base:     u,
		indexURL: strings.TrimSuffix(u.String(), "/") + "/index.json",
		client:   &http.Client{Timeout: fetchTimeout},
		now:      time.Now,
	}, nil
}

// Index returns the firmware index, fetching it when the cached copy is older
// than indexTTL. If a refresh fails, the previous index is served (and the
// failure logged) until a refresh succeeds; with nothing cached, the error is
// returned.
func (c *Catalog) Index(ctx context.Context) (*Index, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.cached != nil && now.Before(c.refreshAt) {
		return c.cached, nil
	}
	// One caller's cancellation (a closed tab) mustn't fail the fetch the
	// callers queued behind it are waiting on.
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
	defer cancel()
	idx, err := c.fetch(fctx)
	if err != nil {
		if c.cached == nil {
			return nil, err
		}
		slog.Warn("firmware: index refresh failed; serving the previous index", "err", err)
		c.refreshAt = now.Add(retryAfterError)
		return c.cached, nil
	}
	c.cached, c.refreshAt = idx, now.Add(indexTTL)
	return idx, nil
}

func (c *Catalog) fetch(ctx context.Context) (*Index, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.indexURL, nil)
	if err != nil {
		return nil, fmt.Errorf("firmware index request: %w", err)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch firmware index: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch firmware index: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxIndexBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read firmware index: %w", err)
	}
	if len(data) > maxIndexBytes {
		return nil, errors.New("firmware index is larger than the limit")
	}
	return parseIndex(data, c.base)
}
