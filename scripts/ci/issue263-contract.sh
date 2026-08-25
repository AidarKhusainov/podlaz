#!/usr/bin/env bash
set -euo pipefail

# Hosted, non-root contract checks for Issue #263. Real installed-package and
# host-network behavior remains in the dedicated self-hosted E2E workflow.
go test ./internal/service -run 'TestSystemdUnit'
go test ./scripts/e2e -run 'TestIssue26(2Workflow|3)'
bash scripts/ci/validate-installed-status-test.sh
