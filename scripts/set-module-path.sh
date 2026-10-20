#!/usr/bin/env bash
# Usage: scripts/set-module-path.sh <new-module-path>
# Rewrites the module path in go.mod and every import of it in *.go files,
# then runs gofmt check, go build, go vet and go test to prove the swap.
# Example: scripts/set-module-path.sh github.com/<user>/frankfurter-go
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
  echo "usage: $0 <new-module-path>" >&2
  exit 2
fi
new="$1"
if [[ ! "$new" =~ ^[A-Za-z0-9._~/-]+$ ]]; then
  echo "error: module path contains unsupported characters: $new" >&2
  exit 2
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
old="$(go list -m)"
if [[ "$old" == "$new" ]]; then
  echo "module path already $new; nothing to do"
  exit 0
fi

go mod edit -module "$new"
# Rewrite quoted imports of the old path (exact prefix match only).
# Paths are passed via the environment so they are never parsed as Perl code.
export OLD="$old" NEW="$new"
# shellcheck disable=SC2016 # $ENV{...} is Perl, expanded by perl, not bash.
find . -name '*.go' -not -path './vendor/*' -print0 |
  xargs -0 perl -pi -e 's{"\Q$ENV{OLD}\E((?:/[^"]*)?)"}{"$ENV{NEW}$1"}g'

gofmt -w .
echo "module: $old -> $new"
test -z "$(gofmt -l .)"
go build ./...
go vet ./...
go test -count=1 ./...
if grep -rn --include='*.go' -F "\"$old" . ; then
  echo "error: references to $old remain" >&2
  exit 1
fi
echo "OK: module path set to $new"
