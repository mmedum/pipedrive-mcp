package pipedrive

import (
	"bytes"
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
// v1 paths are composed internally against the same host via apiPath
// for the rare endpoint that has no v2 equivalent (notes, planned for
// Phase 2). No exported v1 helper exists until an outside caller
// genuinely needs one.
func BaseURL(domain string) string {
	return fmt.Sprintf("https://%s.pipedrive.com/api/v2", domain)
}

// apiVersion picks which Pipedrive API base the request targets. v1 is
// only reachable via internal calls for the few endpoints that have no
// v2 equivalent.
type apiVersion int

const (
	apiV2 apiVersion = iota
	apiV1
)

// Client is a thin wrapper around net/http for the Pipedrive REST API.
// Safe for concurrent use across goroutines. There is one instance per
// process; the MCP server hands a pointer to every tool's Register()
// function.
type Client struct {
	host        string // https://{domain}.pipedrive.com (no path suffix)
	token       string
	http        *http.Client
	logger      *slog.Logger
	maxAttempts int           // retry attempts for 429 and 5xx; default 3
	baseDelay   time.Duration // base for jittered exponential backoff; default 1s
}

// Options configures a new Client. BaseURL is the v2 base (e.g.
// "https://acme.pipedrive.com/api/v2"); only the scheme+host portion is
// retained — v1 paths are composed against the same host for the
// documented carve-outs.
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
	return &Client{
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

// do issues a request, decoding the response body into out (which may be
// nil for endpoints that return no payload) and applying retry/backoff
// for 429 and 5xx.
func (c *Client) do(ctx context.Context, api apiVersion, method, path string, body, out any) error {
	bodyBytes, err := encodeBody(body)
	if err != nil {
		return err
	}
	requestURL := c.host + apiPath(api) + path

	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		retry, retryAfter, err := c.attempt(ctx, method, requestURL, bodyBytes, out, attempt)
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

// attempt runs a single request. retry indicates whether the caller
// should sleep and try again; when false, err is the terminal result
// (nil on success).
func (c *Client) attempt(
	ctx context.Context,
	method, requestURL string,
	bodyBytes []byte,
	out any,
	attempt int,
) (retry bool, retryAfter time.Duration, err error) {
	req, err := newRequest(ctx, method, requestURL, bodyBytes, c.token)
	if err != nil {
		return false, 0, err
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	duration := time.Since(start)
	if err != nil {
		c.logger.WarnContext(ctx, "pipedrive request failed",
			slog.String("method", method),
			slog.String("url", requestURL),
			slog.Int("attempt", attempt+1),
			slog.Duration("duration", duration),
			slog.String("error", err.Error()),
		)
		wrapped := fmt.Errorf("pipedrive: %s %s: %w", method, requestURL, err)
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
		slog.String("method", method),
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

func encodeBody(body any) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("pipedrive: encode body: %w", err)
	}
	return b, nil
}

func newRequest(ctx context.Context, method, requestURL string, bodyBytes []byte, token string) (*http.Request, error) {
	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("pipedrive: build request: %w", err)
	}
	req.Header.Set("x-api-token", token)
	req.Header.Set("Accept", "application/json")
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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

func apiPath(api apiVersion) string {
	if api == apiV1 {
		return "/api/v1"
	}
	return "/api/v2"
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

// readBody reads up to 1 MiB. Pipedrive list responses with limit=500 plus
// heavy custom fields can plausibly exceed this; revisit before Phase 1
// list endpoints land.
func readBody(r io.Reader) ([]byte, error) {
	const limit = 1 << 20
	return io.ReadAll(io.LimitReader(r, limit))
}
