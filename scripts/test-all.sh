#!/usr/bin/env bash
set -euo pipefail

PROJECT_ROOT="$(realpath "$( dirname "${BASH_SOURCE[0]}" )/../")"
pushd "$PROJECT_ROOT"

go build -o querygen ./cmd/querygen
./querygen ./...
go test -race -v ./...
