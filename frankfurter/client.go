// Package frankfurter is a small, typed Go client for the Frankfurter
// exchange-rate API (https://frankfurter.dev). It needs no API key.
//
// The client:
//   - takes a context.Context on every call
//   - applies a default 10-second HTTP timeout
//   - returns a typed *APIError for documented {status, message} errors
//   - retries transient failures (network errors, 429, 5xx) with capped
//     exponential backoff and full jitter, honouring Retry-After
//
// It uses only the default blended-rate endpoints. Frankfurter states those
// are free to use commercially; provider-specific endpoints are subject to
// each provider's terms and are deliberately not wrapped here.
package frankfurter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// DefaultBaseURL is the public Frankfurter instance.
const DefaultBaseURL = "https://api.frankfurter.dev"

const dateLayout = "2006-01-02"

var currencyRE = regexp.MustCompile(`^[A-Za-z]{3}$`)

// Rate is one exchange-rate observation.
type Rate struct {
	Date  Date    `json:"date"`
	Base  string  `json:"base"`
	Quote string  `json:"quote"`
	Rate  float64 `json:"rate"`
}

// Currency describes a supported currency.
type Currency struct {
	ISOCode    string `json:"iso_code"`
	ISONumeric string `json:"iso_numeric"`
	Name       string `json:"name"`
	Symbol     string `json:"symbol"`
	StartDate  Date   `json:"start_date"`
	EndDate    Date   `json:"end_date"`
}

// Date is a calendar date (YYYY-MM-DD) in UTC.
type Date struct{ time.Time }

// UnmarshalJSON parses "YYYY-MM-DD".
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		d.Time = time.Time{}
		return nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return fmt.Errorf("frankfurter: bad date %q: %w", s, err)
	}
	d.Time = t
	return nil
}

// String returns the date as YYYY-MM-DD.
func (d Date) String() string { return d.Format(dateLayout) }

// APIError is a non-2xx response. Frankfurter documents the body as
// {"status": <int>, "message": <string>}.
type APIError struct {
	StatusCode int    // HTTP status code
	Message    string // "message" from the body, or a snippet of the raw body
}

func (e *APIError) Error() string {
	return fmt.Sprintf("frankfurter: HTTP %d: %s", e.StatusCode, e.Message)
}

// Temporary reports whether the error is worth retrying.
func (e *APIError) Temporary() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// RetryPolicy controls retries of transient failures.
type RetryPolicy struct {
	MaxAttempts int           // total attempts including the first; 1 disables retries
	BaseDelay   time.Duration // first backoff ceiling
	MaxDelay    time.Duration // cap for any single wait (also caps Retry-After)
}

// DefaultRetryPolicy makes up to 3 attempts with 200ms, 400ms ceilings, capped at 5s.
var DefaultRetryPolicy = RetryPolicy{MaxAttempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 5 * time.Second}

// Client is safe for concurrent use.
type Client struct {
	baseURL    string
	httpClient *http.Client
	userAgent  string
	retry      RetryPolicy
	sleep      func(context.Context, time.Duration) error
	jitter     func(time.Duration) time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL points the client at another instance (e.g. self-hosted or a test server).
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient replaces the default *http.Client (10s timeout).
// A nil client is ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.httpClient = h
		}
	}
}

// WithUserAgent sets the User-Agent header.
func WithUserAgent(ua string) Option { return func(c *Client) { c.userAgent = ua } }

// WithRetryPolicy overrides DefaultRetryPolicy.
func WithRetryPolicy(p RetryPolicy) Option { return func(c *Client) { c.retry = p } }

// New returns a Client for the public API unless overridden by options.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Timeout: 10 * time.Second},
		userAgent:  "frankfurter-go/0.1.0 (+https://github.com/anti-gravity-bit/frankfurter-go)",
		retry:      DefaultRetryPolicy,
		sleep:      sleepCtx,
		jitter:     func(d time.Duration) time.Duration { return time.Duration(rand.Int63n(int64(d) + 1)) },
	}
	for _, o := range opts {
		o(c)
	}
	if c.retry.MaxAttempts < 1 {
		c.retry.MaxAttempts = 1
	}
	return c
}

// Rate returns the latest rate for base→quote. Pass a non-zero date for a
// historical rate.
func (c *Client) Rate(ctx context.Context, base, quote string, date time.Time) (Rate, error) {
	if err := validateCodes(base, quote); err != nil {
		return Rate{}, err
	}
	q := url.Values{}
	if !date.IsZero() {
		q.Set("date", date.Format(dateLayout))
	}
	var r Rate
	err := c.get(ctx, "/v2/rate/"+strings.ToUpper(base)+"/"+strings.ToUpper(quote), q, &r)
	return r, err
}

// RatesQuery selects rates. All fields are optional.
type RatesQuery struct {
	Base   string
	Quotes []string
	Date   time.Time // single historical day
	From   time.Time // time series start (inclusive)
	To     time.Time // time series end (inclusive); requires From
}

// Rates returns one or more observations matching q.
func (c *Client) Rates(ctx context.Context, q RatesQuery) ([]Rate, error) {
	v := url.Values{}
	if q.Base != "" {
		if err := validateCodes(q.Base); err != nil {
			return nil, err
		}
		v.Set("base", strings.ToUpper(q.Base))
	}
	if len(q.Quotes) > 0 {
		if err := validateCodes(q.Quotes...); err != nil {
			return nil, err
		}
		up := make([]string, len(q.Quotes))
		for i, s := range q.Quotes {
			up[i] = strings.ToUpper(s)
		}
		v.Set("quotes", strings.Join(up, ","))
	}
	switch {
	case !q.Date.IsZero() && !q.From.IsZero():
		return nil, errors.New("frankfurter: use either Date or From/To, not both")
	case !q.To.IsZero() && q.From.IsZero():
		return nil, errors.New("frankfurter: To requires From")
	case !q.From.IsZero() && !q.To.IsZero() && q.To.Before(q.From):
		return nil, errors.New("frankfurter: To is before From")
	}
	if !q.Date.IsZero() {
		v.Set("date", q.Date.Format(dateLayout))
	}
	if !q.From.IsZero() {
		v.Set("from", q.From.Format(dateLayout))
	}
	if !q.To.IsZero() {
		v.Set("to", q.To.Format(dateLayout))
	}
	var out []Rate
	err := c.get(ctx, "/v2/rates", v, &out)
	return out, err
}

// Currencies lists the currencies the API supports.
func (c *Client) Currencies(ctx context.Context) ([]Currency, error) {
	var out []Currency
	err := c.get(ctx, "/v2/currencies", nil, &out)
	return out, err
}

// Convert multiplies amount by the latest base→quote rate. Frankfurter has no
// conversion endpoint; float64 is fine for display, not for accounting.
func (c *Client) Convert(ctx context.Context, amount float64, base, quote string) (float64, Rate, error) {
	r, err := c.Rate(ctx, base, quote, time.Time{})
	if err != nil {
		return 0, Rate{}, err
	}
	return amount * r.Rate, r, nil
}

func validateCodes(codes ...string) error {
	for _, s := range codes {
		if !currencyRE.MatchString(s) {
			return fmt.Errorf("frankfurter: invalid currency code %q (want 3 letters, e.g. EUR)", s)
		}
	}
	return nil
}

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := c.baseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var lastErr error
	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		retryAfter, err := c.do(ctx, u, out)
		if err == nil {
			return nil
		}
		lastErr = err
		if cerr := ctx.Err(); cerr != nil {
			// The deadline or cancellation landed during this attempt. Report it
			// even though the attempt itself returned an HTTP error, so callers
			// can rely on errors.Is(err, context.DeadlineExceeded/Canceled).
			if errors.Is(err, cerr) {
				return err
			}
			return errors.Join(err, cerr)
		}
		if attempt == c.retry.MaxAttempts || !retryable(ctx, err) {
			break
		}
		wait := c.backoff(attempt, retryAfter)
		if serr := c.sleep(ctx, wait); serr != nil {
			return errors.Join(lastErr, serr)
		}
	}
	return lastErr
}

// do performs one attempt. It returns any Retry-After hint from the response.
func (c *Client) do(ctx context.Context, u string, out any) (time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("frankfurter: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("frankfurter: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return 0, fmt.Errorf("frankfurter: read body: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		var doc struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &doc) == nil && doc.Message != "" {
			apiErr.Message = snippet([]byte(doc.Message))
		} else {
			apiErr.Message = snippet(body)
		}
		return parseRetryAfter(resp.Header.Get("Retry-After")), apiErr
	}
	if err := json.Unmarshal(body, out); err != nil {
		return 0, fmt.Errorf("frankfurter: decode response: %w", err)
	}
	return 0, nil
}

func retryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false // caller cancelled or deadline passed
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Temporary()
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF)
}

func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return min(retryAfter, c.retry.MaxDelay)
	}
	ceiling := c.retry.BaseDelay << (attempt - 1)
	if ceiling <= 0 || ceiling > c.retry.MaxDelay {
		ceiling = c.retry.MaxDelay
	}
	return c.jitter(ceiling)
}

func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// snippet makes server-supplied text safe to put in an error or log line:
// invalid UTF-8 and control characters are dropped (no log/terminal
// injection), newlines and tabs become spaces, and the result is capped at
// 200 runes without splitting a multi-byte character.
func snippet(b []byte) string {
	s := strings.ToValidUTF8(string(b), "")
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200]) + "…"
	}
	if s == "" {
		s = "(empty body)"
	}
	return s
}
