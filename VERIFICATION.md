# Verification (v0.1.0 release check, 20 Oct 2026, IST)

Every command here was really run on Linux amd64 with go1.27.2, plus go1.22.0 for the minimum version.

## Offline checks (same commands as `.github/workflows/ci.yml`)
```
$ gofmt -l .                       # (no output)
$ go mod tidy                      # go.mod unchanged; no go.sum (no dependencies)
$ go build ./...                   # exit 0
$ go vet ./...                     # exit 0
$ go test -race -count=1 -covermode=atomic -coverprofile=coverage.out ./...
ok  github.com/anti-gravity-bit/frankfurter-go/cmd/fx        coverage: 74.5% of statements
ok  github.com/anti-gravity-bit/frankfurter-go/frankfurter   coverage: 89.2% of statements
$ go tool cover -func=coverage.out | tail -1
total:  (statements)  85.4%
```
39 passing tests (13 top-level offline tests plus subtests), 0 failures. `GOTOOLCHAIN=go1.22.0 go test -race ./...` → ok.
Flake check: 8 parallel processes × `go test -race -count=60 ./...` → all ok.

## CLI (live API, 20 Oct 2026 18:23 IST; rates change daily)
```
$ go install ./cmd/fx && fx rate EUR INR
2026-10-20 1 EUR = 106.47 INR
$ go run ./cmd/fx convert 4999 INR USD
4999 INR = 52.69 USD (rate 0.01054, 2026-10-20)
$ go run ./cmd/fx rates -base EUR -quotes USD,INR,GBP
2026-10-20 EUR/GBP 0.84202
2026-10-20 EUR/INR 106.47
2026-10-20 EUR/USD 1.1227
```

## Live test
```
$ go test -tags live -run Live -v ./frankfurter
=== RUN   TestLiveRateAndError
    live_test.go:26: live: 1 EUR = 106.4700 INR on 2026-10-20
    live_test.go:33: live: documented error surfaced as frankfurter: HTTP 422: invalid currency: ABC
--- PASS: TestLiveRateAndError (0.51s)
```

## Static analysis
staticcheck: 0 findings. govulncheck: no vulnerabilities found. gitleaks: no leaks (working tree and git history).
