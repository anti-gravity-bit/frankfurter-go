# Security

This client talks to a public, no-key API, so it handles no credentials. The hardening is about log hygiene and robustness:

- Error messages from the server are sanitised before they reach `APIError`: control characters are stripped (no log or terminal injection), newlines become spaces, the text is kept as valid UTF-8, and it is capped at 200 characters.
- Response bodies are read with an 8 MiB cap.
- Inputs are validated (3-letter currency codes) before any URL is built.
- `Retry-After` is capped by `MaxDelay`, and `4xx` responses are never retried.
- A password in a custom base URL is redacted from transport errors by `net/http`. A test covers this.
- There are no third-party dependencies. `govulncheck ./...` reports no vulnerabilities as of the v0.1.0 release check.

To report a problem, open a GitHub issue. Don't include secrets in it.
