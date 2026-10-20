# frankfurter-go: a production-style Go quickstart and client

> **Portfolio sample by Abir Sarkar** (API Documentation & Developer Experience Studio).
> It shows what I deliver to API companies: a quickstart that gets a Go developer from zero to a working, well-behaved integration in minutes, plus the small typed client and tests behind it.

This is an **unofficial** client for the free [Frankfurter](https://frankfurter.dev) exchange-rate API. Frankfurter has official guides for Python and JavaScript but none for Go, so this fills that gap. No API key is needed.

## Quickstart (5 minutes)

**Prerequisites:** Go 1.22 or newer (the minimum is set in `go.mod`; verified with go1.22.0 and go1.27.2). Internet access for the CLI and the live test only. No API key, no third-party modules, so there is no `go.sum`.

### Install the CLI
```bash
go install github.com/anti-gravity-bit/frankfurter-go/cmd/fx@latest
fx rate EUR INR
```
`go install` puts `fx` in `$(go env GOPATH)/bin` (or `$GOBIN`). Make sure that directory is on your `PATH`.

### Or run from a clone
```bash
git clone https://github.com/anti-gravity-bit/frankfurter-go.git && cd frankfurter-go
go run ./cmd/fx rate EUR INR
# 2026-10-20 1 EUR = 106.5 INR
go run ./cmd/fx convert 4999 INR USD
# 4999 INR = 52.69 USD (rate 0.01054, 2026-10-20)
go run ./cmd/fx rates -base EUR -quotes USD,INR,GBP
# 2026-10-20 EUR/GBP 0.84207
# 2026-10-20 EUR/INR 106.5
# 2026-10-20 EUR/USD 1.1228
```
Rates change daily, so your numbers will differ.

### Use it as a library
```bash
go get github.com/anti-gravity-bit/frankfurter-go/frankfurter
```

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/anti-gravity-bit/frankfurter-go/frankfurter"
)

func main() {
	c := frankfurter.New() // 10s timeout, 3 attempts with backoff

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r, err := c.Rate(ctx, "EUR", "INR", time.Time{}) // zero time = latest
	var apiErr *frankfurter.APIError
	switch {
	case errors.As(err, &apiErr):
		log.Printf("API said %d: %s", apiErr.StatusCode, apiErr.Message) // e.g. 422 invalid currency: ABC
	case err != nil:
		log.Printf("network/timeout: %v", err)
	default:
		fmt.Printf("1 %s = %g %s on %s\n", r.Base, r.Rate, r.Quote, r.Date)
	}
}
```

## What "production-quality" means here
| Concern | How it's handled |
|---|---|
| Timeouts | Every call takes a `context.Context`. The default `http.Client` has a 10s timeout. |
| Errors | Documented `{status, message}` bodies become a typed `*APIError`. Non-JSON error bodies are captured as a snippet. Malformed success bodies return a decode error and never a zero value. |
| Retries | Network errors, `429` and `5xx` are retried with capped exponential backoff and full jitter. `Retry-After` is honoured, up to `MaxDelay`. **`4xx` is never retried.** Context cancellation stops retries immediately. |
| Input validation | Currency codes are checked locally (3 letters). Conflicting date options are rejected before any request is made. |
| Dependencies | Standard library only. |
| Tests | Table-driven tests against `httptest` servers: happy paths, documented 422, 404, retry/backoff timing, Retry-After, network failure, deadlines, query building and the CLI. Race detector clean. |
| Live check | One opt-in integration test behind the build tag `live`. |

## API covered
| Method | Endpoint |
|---|---|
| `Rate(ctx, base, quote, date)` | `GET /v2/rate/{base}/{quote}[?date=]` |
| `Rates(ctx, RatesQuery{...})` | `GET /v2/rates?base=&quotes=&date=` or `from=&to=` |
| `Currencies(ctx)` | `GET /v2/currencies` |
| `Convert(ctx, amount, base, quote)` | Client-side: amount × latest rate. Frankfurter has no conversion endpoint. `float64` is for display only, not accounting. |

## Running the checks
```bash
go vet ./...
gofmt -l .                                       # prints nothing when formatted
go test -race ./...                              # offline; no network needed
go test -tags live -run Live -v ./frankfurter    # opt-in: calls api.frankfurter.dev
```
See [VERIFICATION.md](VERIFICATION.md) for real output from these commands and [SECURITY.md](SECURITY.md) for the security notes. CI (`.github/workflows/ci.yml`) runs the same offline checks plus coverage; the live test runs only when triggered manually.

Forking? Switch the module path in one step: `scripts/set-module-path.sh github.com/<you>/frankfurter-go`.

## Common mistakes this client prevents
- Treating a `422 {"message":"invalid currency: ABC"}` as a successful zero rate.
- Retrying a `400`/`422` forever, or retrying after the caller's deadline.
- Hammering the API on `429` instead of backing off.
- Using `http.DefaultClient`, which has no timeout.

## Terms & attribution
- Data: [Frankfurter](https://frankfurter.dev). As of 20 Oct 2026 its FAQ says the API is free for commercial use: "Blended rates are Frankfurter's own calculation and free to use. Direct queries to individual provider endpoints remain subject to each provider's terms." This client only calls the blended endpoints. See https://frankfurter.dev/license/ for provider terms and the no-warranty disclaimer.
- Rates are informational. For tax or accounting, check the provider's own publication.
- This project isn't affiliated with or endorsed by Frankfurter.
- Code: MIT (see [LICENSE](LICENSE)).

## About the author
Abir Sarkar is a senior backend engineer (Go, APIs, technical documentation) based in Kolkata, India. I help API-first companies fix developer onboarding with docs audits, Go quickstarts, SDK README refreshes and error references. Contact: GitHub [@anti-gravity-bit](https://github.com/anti-gravity-bit).
