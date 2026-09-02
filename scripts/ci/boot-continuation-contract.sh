#!/usr/bin/env bash
set -euo pipefail

# Hosted, non-root contract checks for boot continuation and reconciliation.
# Privileged installed-package behavior remains covered by the self-hosted E2E workflow.
go test ./internal/service -run 'TestSystemdUnit' -count=1
go test ./scripts/e2e -run 'Test(NetworkReconciliationWorkflow|BootContinuation)' -count=1
bash scripts/ci/validate-installed-status-test.sh
