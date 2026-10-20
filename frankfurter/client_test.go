package frankfurter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client pointed at srv with instant, recorded sleeps.
func newTestClient(srv *httptest.Server, attempts int) (*Client, *[]time.Duration) {
	var slept []time.Duration
	c := New(WithBaseURL(srv.URL), WithRetryPolicy(RetryPolicy{MaxAttempts: attempts, BaseDelay: 100 * time.Millisecond, MaxDelay: time.Second}))
	c.sleep = func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return ctx.Err() }
	c.jitter = func(d time.Duration) time.Duration { return d } // deterministic: use the ceiling
	return c, &slept
}

func TestRate(t *testing.T) {
	tests := []struct {
		name       string
		base, quot string
		date       time.Time
		status     int
		body       string
		wantPath   string
		wantQuery  string
		wantRate   float64
		wantErrMsg string
		wantStatus int
	}{
		{name: "latest", base: "eur", quot: "inr", status: 200,
			body:     `{"date":"2026-10-20","base":"EUR","quote":"INR","rate":106.5}`,
			wantPath: "/v2/rate/EUR/INR", wantRate: 106.5},
		{name: "historical", base: "EUR", quot: "USD", date: time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC), status: 200,
			body:     `{"date":"2024-05-15","base":"EUR","quote":"USD","rate":1.0836}`,
			wantPath: "/v2/rate/EUR/USD", wantQuery: "date=2024-05-15", wantRate: 1.0836},
		{name: "documented 422 error", base: "EUR", quot: "ABC", status: 422,
			body:     `{"status":422,"message":"invalid currency: ABC"}`,
			wantPath: "/v2/rate/EUR/ABC", wantErrMsg: "invalid currency: ABC", wantStatus: 422},
		{name: "404 not retried", base: "EUR", quot: "XYZ", status: 404,
			body:     `{"status":404,"message":"not found"}`,
			wantPath: "/v2/rate/EUR/XYZ", wantErrMsg: "not found", wantStatus: 404},
		{name: "invalid code rejected locally", base: "EURO", quot: "USD", wantErrMsg: "invalid currency code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != tc.wantPath || r.URL.RawQuery != tc.wantQuery {
					t.Errorf("got %s?%s, want %s?%s", r.URL.Path, r.URL.RawQuery, tc.wantPath, tc.wantQuery)
				}
				if r.Header.Get("Accept") != "application/json" || r.Header.Get("User-Agent") == "" {
					t.Errorf("missing headers: %v", r.Header)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			c, _ := newTestClient(srv, 3)

			got, err := c.Rate(context.Background(), tc.base, tc.quot, tc.date)
			if tc.wantErrMsg != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrMsg) {
					t.Fatalf("err = %v, want containing %q", err, tc.wantErrMsg)
				}
				var apiErr *APIError
				if tc.wantStatus != 0 && (!errors.As(err, &apiErr) || apiErr.StatusCode != tc.wantStatus) {
					t.Fatalf("want APIError %d, got %#v", tc.wantStatus, err)
				}
				if tc.wantStatus >= 400 && tc.wantStatus < 500 && calls.Load() != 1 {
					t.Fatalf("4xx retried: %d calls", calls.Load())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Rate != tc.wantRate || got.Base != strings.ToUpper(tc.base) || got.Date.IsZero() {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestRetries(t *testing.T) {
	tests := []struct {
		name       string
		statuses   []int // per attempt; last one wins after exhaustion
		retryAfter string
		attempts   int
		wantCalls  int32
		wantErr    bool
		wantSleeps []time.Duration
	}{
		{name: "503 then success", statuses: []int{503, 200}, attempts: 3, wantCalls: 2,
			wantSleeps: []time.Duration{100 * time.Millisecond}},
		{name: "429 honours Retry-After", statuses: []int{429, 200}, retryAfter: "2", attempts: 3, wantCalls: 2,
			wantSleeps: []time.Duration{time.Second}}, // 2s capped at MaxDelay=1s
		{name: "exhausts attempts with exponential backoff", statuses: []int{500, 502, 504}, attempts: 3, wantCalls: 3, wantErr: true,
			wantSleeps: []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}},
		{name: "retries disabled", statuses: []int{503}, attempts: 1, wantCalls: 1, wantErr: true},
		{name: "400 is not retried", statuses: []int{400}, attempts: 3, wantCalls: 1, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := int(calls.Add(1)) - 1
				st := tc.statuses[min(n, len(tc.statuses)-1)]
				if st != 200 {
					if tc.retryAfter != "" {
						w.Header().Set("Retry-After", tc.retryAfter)
					}
					w.WriteHeader(st)
					io.WriteString(w, `{"status":`+http.StatusText(st)+`}`) // deliberately not the documented shape
					return
				}
				io.WriteString(w, `{"date":"2026-10-20","base":"EUR","quote":"USD","rate":1.1228}`)
			}))
			defer srv.Close()
			c, slept := newTestClient(srv, tc.attempts)

			_, err := c.Rate(context.Background(), "EUR", "USD", time.Time{})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if calls.Load() != tc.wantCalls {
				t.Fatalf("calls = %d, want %d", calls.Load(), tc.wantCalls)
			}
			if len(*slept) != len(tc.wantSleeps) {
				t.Fatalf("sleeps = %v, want %v", *slept, tc.wantSleeps)
			}
			for i := range tc.wantSleeps {
				if (*slept)[i] != tc.wantSleeps[i] {
					t.Fatalf("sleeps = %v, want %v", *slept, tc.wantSleeps)
				}
			}
		})
	}
}

// roundTripFunc lets tests fail or answer at the transport layer, with no
// sockets. (An earlier version dialled a closed httptest port; under parallel
// load another process can grab that port, which made the test flaky.)
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func refusedTransport() http.RoundTripper {
	return roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused (simulated)")}
	})
}

func TestNetworkErrorIsRetried(t *testing.T) {
	c := New(WithBaseURL("http://127.0.0.1:1"), WithHTTPClient(&http.Client{Transport: refusedTransport()}),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}))
	var sleeps int
	c.sleep = func(ctx context.Context, d time.Duration) error { sleeps++; return nil }
	if _, err := c.Rate(context.Background(), "EUR", "USD", time.Time{}); err == nil {
		t.Fatal("expected error")
	}
	if sleeps != 1 {
		t.Fatalf("sleeps = %d, want 1 (one retry)", sleeps)
	}
}

func TestContextDeadlineStopsRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c := New(WithBaseURL(srv.URL), WithRetryPolicy(RetryPolicy{MaxAttempts: 10, BaseDelay: 50 * time.Millisecond, MaxDelay: 50 * time.Millisecond}))
	// Fixed 50 ms waits. With random full jitter, 9 short waits could
	// occasionally all fit inside the deadline, making this test flaky.
	c.jitter = func(d time.Duration) time.Duration { return d }
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	_, err := c.Rate(ctx, "EUR", "USD", time.Time{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want DeadlineExceeded, got %v", err)
	}
	if n := calls.Load(); n >= 10 {
		t.Fatalf("made %d calls; deadline should stop retries", n)
	}
}

// Deterministic regression test for a race found under load: the deadline
// expired while a 503 was in flight, and the client returned only the 503,
// hiding the context error from errors.Is.
func TestContextEndingDuringAttemptIsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	tr := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		cancel() // the caller's context ends while this attempt is in flight
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	c := New(WithBaseURL("http://127.0.0.1:1"), WithHTTPClient(&http.Client{Transport: tr}),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}))
	_, err := c.Rate(ctx, "EUR", "USD", time.Time{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled in the error chain, got %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 503 {
		t.Fatalf("want the 503 APIError kept in the chain, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after cancellation)", calls)
	}
}

func TestRatesQuery(t *testing.T) {
	d := func(s string) time.Time { tt, _ := time.Parse(dateLayout, s); return tt }
	tests := []struct {
		name      string
		q         RatesQuery
		wantQuery string
		wantErr   string
	}{
		{name: "base and quotes", q: RatesQuery{Base: "eur", Quotes: []string{"usd", "inr"}}, wantQuery: "base=EUR&quotes=USD%2CINR"},
		{name: "time series", q: RatesQuery{Base: "EUR", Quotes: []string{"USD"}, From: d("2026-10-01"), To: d("2026-10-03")}, wantQuery: "base=EUR&from=2026-10-01&quotes=USD&to=2026-10-03"},
		{name: "date and from conflict", q: RatesQuery{Date: d("2026-10-01"), From: d("2026-10-01")}, wantErr: "either Date or From"},
		{name: "to without from", q: RatesQuery{To: d("2026-10-01")}, wantErr: "To requires From"},
		{name: "reversed range", q: RatesQuery{From: d("2026-10-05"), To: d("2026-10-01")}, wantErr: "before From"},
		{name: "bad quote", q: RatesQuery{Quotes: []string{"US"}}, wantErr: "invalid currency code"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v2/rates" || r.URL.RawQuery != tc.wantQuery {
					t.Errorf("got %s?%s want /v2/rates?%s", r.URL.Path, r.URL.RawQuery, tc.wantQuery)
				}
				io.WriteString(w, `[{"date":"2026-10-01","base":"EUR","quote":"USD","rate":1.1327},{"date":"2026-10-02","base":"EUR","quote":"USD","rate":1.129}]`)
			}))
			defer srv.Close()
			c, _ := newTestClient(srv, 1)
			got, err := c.Rates(context.Background(), tc.q)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || len(got) != 2 || got[1].Date.String() != "2026-10-02" {
				t.Fatalf("got %+v err=%v", got, err)
			}
		})
	}
}

func TestConvertAndCurrencies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/rate/EUR/INR":
			io.WriteString(w, `{"date":"2026-10-20","base":"EUR","quote":"INR","rate":106.5}`)
		case "/v2/currencies":
			io.WriteString(w, `[{"iso_code":"INR","iso_numeric":"356","name":"Indian Rupee","symbol":"₹","start_date":"1973-01-02","end_date":"2026-10-20"}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(srv, 1)
	amt, r, err := c.Convert(context.Background(), 10, "EUR", "INR")
	if err != nil || amt != 1065 || r.Quote != "INR" {
		t.Fatalf("Convert = %v %+v %v", amt, r, err)
	}
	cs, err := c.Currencies(context.Background())
	if err != nil || len(cs) != 1 || cs[0].Symbol != "₹" || cs[0].StartDate.Year() != 1973 {
		t.Fatalf("Currencies = %+v %v", cs, err)
	}
}

func TestMalformedSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"date":"20-10-2026"}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(srv, 1)
	if _, err := c.Rate(context.Background(), "EUR", "USD", time.Time{}); err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{{"", 0}, {"3", 3 * time.Second}, {"-1", 0}, {"soon", 0}}
	for _, tc := range tests {
		if got := parseRetryAfter(tc.in); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
