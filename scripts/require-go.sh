#!/usr/bin/env bash
#
# Fails with an actionable message when the host Go toolchain is missing or older
# than this repo needs. Only `lint`/`generate`/`contract-check` call this --
# `seed`/`test` run Go inside the `gotools` container (see docker-compose.yml)
# precisely so the graded `make up && make seed && make test` contract only
# needs Docker on the host, not a host Go install.
#
# The required version is READ FROM go.mod and is not duplicated here. go.mod is
# the single source of truth: this script parses it, and CI's setup-go step uses
# `go-version-file: go.mod`. Bumping Go is then one line, with nothing to forget.

set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
gomod="$root/go.mod"

if [ ! -f "$gomod" ]; then
    echo "require-go: no go.mod at $gomod" >&2
    exit 1
fi

required="$(awk '$1 == "go" { print $2; exit }' "$gomod")"
if [ -z "$required" ]; then
    echo "require-go: could not read the 'go' directive from $gomod" >&2
    exit 1
fi

if ! command -v go >/dev/null 2>&1; then
    cat >&2 <<EOF
require-go: Go is not installed, or is not on PATH.

'make lint'/'make generate'/'make contract-check' run on the host, so it needs
Go $required or newer. Install it from https://go.dev/dl/ and re-run.
EOF
    exit 1
fi

# GOVERSION is the toolchain actually in use, e.g. "go1.23.0". go.mod carries the
# bare number. Normalise both to three components so 1.23 and 1.23.0 compare equal.
normalise() {
    local v="${1#go}"
    case "$(echo "$v" | tr -cd '.' | wc -c | tr -d ' ')" in
        0) v="$v.0.0" ;;
        1) v="$v.0" ;;
    esac
    echo "$v"
}

have="$(normalise "$(go env GOVERSION)")"
want="$(normalise "$required")"

# sort -V puts the lower version first; if that is not the required one, the host
# toolchain is too old.
if [ "$(printf '%s\n%s\n' "$want" "$have" | sort -V | head -n1)" != "$want" ]; then
    cat >&2 <<EOF
require-go: host Go is $have, but go.mod requires $want or newer.

Upgrade from https://go.dev/dl/, then re-run. (Do not lower the 'go' directive in
go.mod to work around this -- the pinned dependency set is chosen to match it.)
EOF
    exit 1
fi
