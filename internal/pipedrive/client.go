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
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// BaseURL returns the v2 base URL for a Pipedrive workspace subdomain.
// The path suffix is stripped by hostOf in New, so the per-call API
// version (v2 by default; v1 for the notes carve-out) is composed in
// exec — see the apiVersion type below.
func BaseURL(domain string) string {
	return fmt.Sprintf("https://%s.pipedrive.com/api/v2", domain)
}

// apiVersion is the typed enum for the per-call API path segment.
// String-typing rather than free-form "v1"/"v2" prevents a typo at a
// new call site from compiling into a malformed request URL.
type apiVersion string

const (
	apiV1 apiVersion = "v1"
	apiV2 apiVersion = "v2"
)

// Client is a thin wrapper around net/http for the Pipedrive REST API.
// Safe for concurrent use across goroutines. There is one instance per
// process; the MCP server hands a pointer to every tool's Register()
// function.
//
// The per-resource field caches (dealFields, …) live on the Client
// because their lifetime matches the process and they reuse the
// Client's transport. Callers go through the typed accessor methods
// (ResolveDealCustomFields, WarmDealFields, ReloadDealFields) rather
// than the unexported field.
type Client struct {
	host        string // https://{domain}.pipedrive.com (no path suffix)
	token       string
	http        *http.Client
	logger      *slog.Logger
	maxAttempts int           // attempts per call, the first included; default 3
	baseDelay   time.Duration // base for jittered exponential backoff; default 1s

	meMu   sync.Mutex // guards me, meErr and meDone
	me     *User      // memoized whoami; see WhoAmI
	meErr  error      // the memoized WhoAmI's error, replayed to later callers
	meDone bool       // whether me or meErr holds an answer

	dealFields         *FieldCache // lazy-loaded; first ListDeals/GetDeal triggers fetch
	personFields       *FieldCache // lazy-loaded; first GetPerson triggers fetch
	organizationFields *FieldCache // lazy-loaded; first GetOrganization triggers fetch
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
	c.dealFields = NewFieldCache(c.ListDealFields)
	c.personFields = NewFieldCache(c.ListPersonFields)
	c.organizationFields = NewFieldCache(c.ListOrganizationFields)
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

// do issues a GET against /api/v2 + path and decodes the response body
// into out (which may be nil).
func (c *Client) do(ctx context.Context, path string, out any) error {
	return c.exec(ctx, http.MethodGet, apiV2, path, nil, out)
}

// doV1 is `do` on /api/v1. Only notes and WhoAmI use v1, because v2 has
// no /notes and no /users. A new v1 caller needs the same reason, said
// at its call site and in a CHANGELOG entry under ### Changed, and the
// owner's go-ahead.
func (c *Client) doV1(ctx context.Context, path string, out any) error {
	return c.exec(ctx, http.MethodGet, apiV1, path, nil, out)
}

// postV1 is a POST on /api/v1, which creates a note. Like every write it
// is retried only on a 429 or a failed dial: after a 5xx the row may
// exist, and sending it again could create a second one.
func (c *Client) postV1(ctx context.Context, path string, body, out any) error {
	return c.exec(ctx, http.MethodPost, apiV1, path, body, out)
}

// postV2 is a POST on /api/v2, retried as postV1 is.
func (c *Client) postV2(ctx context.Context, path string, body, out any) error {
	return c.exec(ctx, http.MethodPost, apiV2, path, body, out)
}

// patchV2 is a PATCH on /api/v2, Pipedrive's partial update: a field
// the body omits keeps its stored value. That is why every
// Update*Request field is a pointer with omitempty: nil omits, and a
// pointer sends what it points at. Retried as postV1 is.
func (c *Client) patchV2(ctx context.Context, path string, body, out any) error {
	return c.exec(ctx, http.MethodPatch, apiV2, path, body, out)
}

// putV1 is a PUT on /api/v1, which updates a note. v1 treats it as a
// partial update: a field the body omits keeps its stored value. Retried
// as postV1 is, since Pipedrive's reference does not promise the PUT is
// idempotent.
func (c *Client) putV1(ctx context.Context, path string, body, out any) error {
	return c.exec(ctx, http.MethodPut, apiV1, path, body, out)
}

// deleteV1 is a DELETE on /api/v1, which deletes a note. Retried as
// postV1 is: after a 5xx the note may be gone, and a retry's 404 would
// hide the failure that mattered.
func (c *Client) deleteV1(ctx context.Context, path string, out any) error {
	return c.exec(ctx, http.MethodDelete, apiV1, path, nil, out)
}

// deleteV2 is a DELETE on /api/v2, retried as deleteV1 is. A v2 delete
// is soft, and Pipedrive purges the record after 30 days (DeleteDeal).
func (c *Client) deleteV2(ctx context.Context, path string) error {
	// No out parameter: the response carries only the id. A caller that
	// wants the record's final state reads it before deleting.
	return c.exec(ctx, http.MethodDelete, apiV2, path, nil, nil)
}

// exec runs the configured retry loop for a single API call. version
// selects the path segment between /api and the resource path.
func (c *Client) exec(ctx context.Context, method string, version apiVersion, path string, body, out any) error {
	requestURL := c.host + "/api/" + string(version) + path

	var lastErr error
	for attempt := 0; attempt < c.maxAttempts; attempt++ {
		retry, retryAfter, err := c.attempt(ctx, method, version, requestURL, body, out, attempt)
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
	method string,
	version apiVersion,
	requestURL string,
	body, out any,
	attempt int,
) (retry bool, retryAfter time.Duration, err error) {
	req, err := newRequest(ctx, method, requestURL, body, c.token)
	if err != nil {
		return false, 0, err
	}

	start := time.Now()
	resp, err := c.http.Do(req)
	duration := time.Since(start)
	if err != nil {
		// *url.Error repeats the whole URL, query string included, and a
		// search term travels there; the path is enough to say which call.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		c.logger.WarnContext(ctx, "pipedrive request failed",
			slog.String("path", pathOf(requestURL)),
			slog.String("method", method),
			slog.Int("attempt", attempt+1),
			slog.Duration("duration", duration),
			slog.String("error", err.Error()),
		)
		retry := shouldRetryNetwork(method, err)
		if !retry && method != http.MethodGet && ctx.Err() == nil {
			err = fmt.Errorf("%w; the write may have been applied, so read the record before trying again", err)
		}
		wrapped := fmt.Errorf("pipedrive: %s %s: %w", method, pathOf(requestURL), err)
		if retry && attempt+1 < c.maxAttempts {
			return true, 0, wrapped
		}
		return false, 0, wrapped
	}

	rawBody, err := readBody(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return false, 0, fmt.Errorf("pipedrive: read body: %w", err)
	}

	c.logger.DebugContext(ctx, "pipedrive response",
		slog.String("path", pathOf(requestURL)),
		slog.String("method", method),
		slog.Int("status", resp.StatusCode),
		slog.Duration("duration", duration),
	)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, 0, decodeSuccess(rawBody, out)
	}

	apiErr := classifyResponse(resp.StatusCode, version, requestURL, rawBody)
	if attempt+1 < c.maxAttempts && shouldRetry(method, resp.StatusCode) {
		// Pipedrive's rate-limit page documents x-ratelimit-* headers and
		// no Retry-After; one that does arrive is the floor of the wait.
		// https://pipedrive.readme.io/docs/core-api-concepts-rate-limiting
		return true, parseRetryAfter(resp.Header.Get("Retry-After")), apiErr
	}
	return false, 0, apiErr
}

// shouldRetry is the retry policy for an answer. A 429 is retried for
// every method: Pipedrive's rate-limit page says a request over the
// limit is rejected, so it was not applied. A 5xx is retried on a GET
// alone, since a write may have committed before the error.
func shouldRetry(method string, status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if method == http.MethodGet && status >= 500 {
		return true
	}
	return false
}

func newRequest(ctx context.Context, method, requestURL string, body any, token string) (*http.Request, error) {
	var bodyReader io.Reader = http.NoBody
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("pipedrive: encode body: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("pipedrive: build request: %w", err)
	}
	req.Header.Set("x-api-token", token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
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

func classifyResponse(status int, version apiVersion, requestURL string, raw []byte) error {
	var env envelope
	// classify tolerates an empty envelope. A body that is not JSON is an
	// edge's error page: Pipedrive's rate-limit page says a client that
	// keeps going after 429s gets an HTML 403 from Cloudflare. Its length
	// is reported and not the body, which may echo PII.
	// https://pipedrive.readme.io/docs/core-api-concepts-rate-limiting
	nonJSON := ""
	if len(raw) > 0 && json.Unmarshal(raw, &env) != nil {
		nonJSON = fmt.Sprintf("non-JSON response (%d bytes)", len(raw))
	}
	apiErr := classify(status, version, pathOf(requestURL), env)
	if apiErr != nil && apiErr.Message == "" {
		apiErr.Message = nonJSON
	}
	if apiErr == nil {
		return fmt.Errorf("pipedrive: unexpected status %d", status)
	}
	return apiErr
}

// pathOf returns the path portion of a URL, for the log and
// APIError.Endpoint: the query string can carry a search term. Falls
// back to the input on a parse failure, which a URL built here never
// produces.
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

func shouldRetryNetwork(method string, err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if method == http.MethodGet {
		return true
	}
	// A write is repeated only when it never left. A failed dial, DNS
	// included, is a *net.OpError with Op "dial", and nothing was sent;
	// after that a lost answer may follow a commit, which is also why
	// net/http replays a write only when nothing was written (D-54).
	// https://cs.opensource.google/go/go/+/refs/tags/go1.27.3:src/net/dial.go;l=810
	// https://cs.opensource.google/go/go/+/refs/tags/go1.27.3:src/net/http/transport.go;l=844
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// readBody reads up to 8 MiB, so a runaway upstream cannot pin memory.
// The largest answers are the *Fields metadata, which carry every option
// of every dropdown; a page of 100 deals is well under 1 MiB.
func readBody(r io.Reader) ([]byte, error) {
	const limit = 8 << 20
	return io.ReadAll(io.LimitReader(r, limit))
}
