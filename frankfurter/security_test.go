package frankfurter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestErrorMessageIsSanitizedAndBounded(t *testing.T) {
	tests := map[string]string{
		"json message with ANSI + newline": `{"status":422,"message":"bad\u001b[31m\nFAKE LOG LINE` + strings.Repeat("x", 5000) + `"}`,
		"raw body, multibyte at cut":       strings.Repeat("a", 199) + "₹₹₹" + "\x00\x07",
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnprocessableEntity)
				io.WriteString(w, body)
			}))
			defer srv.Close()
			c := New(WithBaseURL(srv.URL), WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
			_, err := c.Rate(context.Background(), "EUR", "USD", time.Time{})
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("want APIError, got %v", err)
			}
			msg := err.Error()
			if strings.ContainsAny(msg, "\n\r\x1b\x00\x07") {
				t.Fatalf("control characters survived: %q", msg)
			}
			if !utf8.ValidString(msg) {
				t.Fatalf("invalid UTF-8: %q", msg)
			}
			if n := utf8.RuneCountInString(apiErr.Message); n > 201 {
				t.Fatalf("message not bounded: %d runes", n)
			}
		})
	}
}

func TestWithHTTPClientNilIsIgnored(t *testing.T) {
	c := New(WithHTTPClient(nil))
	if c.httpClient == nil || c.httpClient.Timeout != 10*time.Second {
		t.Fatalf("nil option replaced default client: %#v", c.httpClient)
	}
}

func TestTransportErrorRedactsURLPassword(t *testing.T) {
	// The password is built at runtime so no credential-shaped literal is committed.
	pw := strings.Repeat("pw-sentinel-", 2)
	u := &url.URL{Scheme: "http", Host: "127.0.0.1:1", User: url.UserPassword("user", pw)}
	c := New(WithBaseURL(u.String()), WithHTTPClient(&http.Client{Transport: refusedTransport()}),
		WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	_, err := c.Rate(context.Background(), "EUR", "USD", time.Time{})
	if err == nil || strings.Contains(err.Error(), pw) {
		t.Fatalf("transport error leaks URL password: %v", err)
	}
}
