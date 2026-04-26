package pipedrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// BaseURL returns the v2 base URL for a Pipedrive workspace subdomain.
// All Phase 0 + Phase 1 traffic is v2-only; the notes carve-out (Phase
// 2) will introduce v1 paths and at that time we'll reintroduce a
// per-call api-version helper here.
func BaseURL(domain string) string {
	return fmt.Sprintf("https://%s.pipedrive.com/api/v2", domain)
}

// Client is a thin wrapper around net/http for the Pipedrive REST API.
// Safe for concurrent use across goroutines. There is one instance per
// process; the MCP server hands a pointer to every tool's Register()
// function.
//
// The per-resource field caches (DealFields, …) live on the Client
// because their lifetime matches the process and they reuse the
// Client's transport. Persons / organizations / products will add
// siblings here as their PRs land.
type Client struct {
	host        string // https://{domain}.pipedrive.com (no path suffix)
	token       string
	http        *http.Client
	logger      *slog.Logger
	maxAttempts int           // retry attempts for 429 and 5xx; default 3
	baseDelay   time.Duration // base for jittered exponential backoff; default 1s

	DealFields *FieldCache // lazy-loaded; first ListDeals/GetDeal triggers fetch
}

// Options configures a new Client. BaseURL is the v2 base (e.g.
// "https://acme.pipedrive.com/api/v2"); only the scheme+host portion
// is retained — the per-request API path is composed in `do`.
type Options struct {
	BaseURL string
	Token   string
	Timeout time.Duration
	Logger  *slog.Logger
}

// New constructs a Client with sensible defaults for retry policy.
//
// The HTTP client is configured to refuse redirects. Pipedrive's v2
// JSON API does not redirect during normal operation, and Go's default
// redirect-follower forwards custom request headers (including the
// `x-api-token` we attach below) to the redirect target verbatim.
// Returning ErrUseLastResponse short-circuits the follow so a stray 3xx
// surfaces as an "unexpected status" error rather than silently leaking
// the token to whatever host the Location header named.
func New(opts Options) *Client {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	c := &Client{
		host:  hostOf(opts.BaseURL),
		token: opts.Token,
		http: &http.Client{
			Timeout: opts.Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logger:      logger,
		maxAttempts: 3,
		baseDelay:   time.Second,
	}
	c.DealFields = NewFieldCache(c.ListDealFields)
	return c
}

// hostOf strips the API path suffix from a full base URL, leaving only
// scheme://host. We compose the API path ourselves per request so v1 and
// v2 calls share one Client.
func hostOf(base string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.Path = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// do issues a GET request, decoding the response body into out (which
// may be nil for endpoints that return no payload) and applying
// retry/backoff for 429 and 5xx.
//
// Phase 0 + Phase 1 are read-only. The first Phase 2 write tool will
// reintroduce per-call method/body parameters and the encodeBody +
// content-type plumbing they need.
func (c *Client) do(ctx context.Context, path string, out any) error {
	requestURL := c.host + "/api/v2" + path

	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		retry, retryAfter, err := c.attempt(ctx, requestURL, out, attempt)
		if !retry {
			return err
		}
		lastErr = err
		c.sleep(ctx, c.backoff(attempt, retryAfter))
	}

	if lastErr == nil {
		lastErr = errors.New("pipedrive: retries exhausted")
	}
	return lastErr
}

// attempt runs a single GET. retry indicates whether the caller should
// sleep and try again; when false, err is the terminal result (nil on
// success).
func (c *Client) attempt(
	ctx context.Context,
	requestURL string,
	out any,
	attempt int,
) (retry bool, retryAfter time.Duration, err error) {
	req, err := newRequest(ctx, requestURL, c.token)
	if err != nil {
		return false, 0, err
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	duration := time.Since(start)
	if err != nil {
		c.logger.WarnContext(ctx, "pipedrive request failed",
			slog.String("url", requestURL),
			slog.Int("attempt", attempt+1),
			slog.Duration("duration", duration),
			slog.String("error", err.Error()),
		)
		wrapped := fmt.Errorf("pipedrive: GET %s: %w", requestURL, err)
		if shouldRetryNetwork(err) && attempt+1 < c.maxAttempts {
			return true, 0, wrapped
		}
		return false, 0, wrapped
	}

	raw, err := readBody(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return false, 0, fmt.Errorf("pipedrive: read body: %w", err)
	}

	c.logger.DebugContext(ctx, "pipedrive response",
		slog.String("url", requestURL),
		slog.Int("status", resp.StatusCode),
		slog.Duration("duration", duration),
	)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, 0, decodeSuccess(raw, out)
	}

	apiErr := classifyResponse(resp.StatusCode, requestURL, raw)
	if isRetryableStatus(resp.StatusCode) && attempt+1 < c.maxAttempts {
		return true, parseRetryAfter(resp.Header.Get("Retry-After")), apiErr
	}
	return false, 0, apiErr
}

func newRequest(ctx context.Context, requestURL, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("pipedrive: build request: %w", err)
	}
	req.Header.Set("x-api-token", token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func decodeSuccess(raw []byte, out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("pipedrive: decode body: %w", err)
	}
	return nil
}

func classifyResponse(status int, requestURL string, raw []byte) error {
	var env envelope
	_ = json.Unmarshal(raw, &env) // best-effort; classify tolerates empty
	apiErr := classify(status, pathOf(requestURL), env)
	if apiErr == nil {
		return fmt.Errorf("pipedrive: unexpected status %d", status)
	}
	return apiErr
}

func isRetryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

// pathOf returns the path portion of a URL for use in classify's endpoint
// hint. Falls back to the input on parse failure (defensive; we built the
// URL ourselves so parse should always succeed).
func pathOf(u string) string {
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}
	return parsed.Path
}

// backoff returns a jittered backoff. retryAfter (from a 429 Retry-After
// header) is the floor; otherwise base * 2^attempt with ±25% jitter.
func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	const jitterFraction = 0.25
	const maxBackoff = 30 * time.Second

	base := c.baseDelay << attempt
	if base > maxBackoff {
		base = maxBackoff
	}
	jitter := time.Duration((rand.Float64()*2 - 1) * jitterFraction * float64(base))
	delay := base + jitter
	if delay < retryAfter {
		delay = retryAfter
	}
	if delay < 0 {
		delay = 0
	}
	return delay
}

func (c *Client) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	if n, err := strconv.Atoi(h); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(h); err == nil {
		return time.Until(t)
	}
	return 0
}

func shouldRetryNetwork(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

// readBody reads up to 8 MiB. Tool inputs cap list page sizes at 100,
// so a deals page with heavy custom fields tops out a few hundred KiB
// in practice; the cap is generous enough that an unexpected
// /dealFields blob (workspaces with hundreds of custom fields) won't
// truncate either. Hard-limited to keep a runaway upstream from
// pinning memory.
func readBody(r io.Reader) ([]byte, error) {
	const limit = 8 << 20
	return io.ReadAll(io.LimitReader(r, limit))
}
