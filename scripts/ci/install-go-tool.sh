#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <govulncheck|nfpm|actionlint>" >&2
  exit 2
fi

. packaging/package-toolchain.env

case "$1" in
  govulncheck)
    go install "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
    ;;
  nfpm)
    go install "github.com/goreleaser/nfpm/v2/cmd/nfpm@${NFPM_VERSION}"
    ;;
  actionlint)
    go install "github.com/rhysd/actionlint/cmd/actionlint@${ACTIONLINT_VERSION}"
    ;;
  *)
    echo "unsupported Go tool: $1" >&2
    exit 2
    ;;
esac

echo "$(go env GOPATH)/bin" >> "${GITHUB_PATH:-/dev/null}"
