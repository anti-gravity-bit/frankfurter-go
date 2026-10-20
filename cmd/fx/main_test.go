package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anti-gravity-bit/frankfurter-go/frankfurter"
)

func TestRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/rate/EUR/INR":
			io.WriteString(w, `{"date":"2026-10-20","base":"EUR","quote":"INR","rate":106.5}`)
		case "/v2/rates":
			io.WriteString(w, `[{"date":"2026-10-20","base":"EUR","quote":"USD","rate":1.1228}]`)
		default:
			w.WriteHeader(422)
			io.WriteString(w, `{"status":422,"message":"invalid currency: ABC"}`)
		}
	}))
	defer srv.Close()
	c := frankfurter.New(frankfurter.WithBaseURL(srv.URL), frankfurter.WithRetryPolicy(frankfurter.RetryPolicy{MaxAttempts: 1}))

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{"rate", []string{"rate", "EUR", "INR"}, "2026-10-20 1 EUR = 106.5 INR\n", ""},
		{"convert", []string{"convert", "10", "EUR", "INR"}, "10 EUR = 1065.00 INR (rate 106.5, 2026-10-20)\n", ""},
		{"rates", []string{"rates", "-base", "EUR", "-quotes", "USD"}, "2026-10-20 EUR/USD 1.1228\n", ""},
		{"api error", []string{"rate", "EUR", "ABC"}, "", "invalid currency: ABC"},
		{"bad amount", []string{"convert", "ten", "EUR", "INR"}, "", "bad amount"},
		{"bad date", []string{"rates", "-date", "20-10-2026"}, "", "bad date"},
		{"rates positional args rejected", []string{"rates", "EUR", "USD,INR"}, "", "not positional arguments"},
		{"no args", nil, "", "usage"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := run(context.Background(), tc.args, &buf, c)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || buf.String() != tc.want {
				t.Fatalf("got %q err=%v, want %q", buf.String(), err, tc.want)
			}
		})
	}
}
