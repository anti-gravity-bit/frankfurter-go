//go:build live

// Opt-in integration test against the public API:
//
//	go test -tags live -run Live -v ./frankfurter
package frankfurter

import (
	"context"
	"testing"
	"time"
)

func TestLiveRateAndError(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c := New(WithUserAgent("frankfurter-go/0.1.0 (live test; +https://github.com/anti-gravity-bit/frankfurter-go)"))

	r, err := c.Rate(ctx, "EUR", "INR", time.Time{})
	if err != nil {
		t.Fatalf("live Rate: %v", err)
	}
	if r.Base != "EUR" || r.Quote != "INR" || r.Rate <= 0 || r.Date.IsZero() {
		t.Fatalf("unexpected live rate: %+v", r)
	}
	t.Logf("live: 1 %s = %.4f %s on %s", r.Base, r.Rate, r.Quote, r.Date)

	_, err = c.Rate(ctx, "EUR", "ABC", time.Time{})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.StatusCode != 422 {
		t.Fatalf("want 422 APIError for ABC, got %v", err)
	}
	t.Logf("live: documented error surfaced as %v", apiErr)
}
