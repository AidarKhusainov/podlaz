#!/usr/bin/env bash
set -euo pipefail

files="$(gofmt -l .)"
if [ -n "${files}" ]; then
  echo "${files}"
  exit 1
fi

go test ./...
go vet ./...
