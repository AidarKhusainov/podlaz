#!/usr/bin/env bash
set -euo pipefail

tmp="${RUNNER_TEMP:-/tmp}/podlaz-cli-smoke"
rm -rf "${tmp}"
mkdir -p "${tmp}"

go run ./cmd/podlaz version

go run ./cmd/podlaz completion bash > "${tmp}/podlaz.bash"
go run ./cmd/podlaz completion zsh > "${tmp}/_podlaz"
go run ./cmd/podlaz completion fish > "${tmp}/podlaz.fish"

bash -n "${tmp}/podlaz.bash"
zsh -n "${tmp}/_podlaz"
fish --no-config --command "source ${tmp}/podlaz.fish"

grep -F '__complete bash' "${tmp}/podlaz.bash"
grep -F '#compdef podlaz plz' "${tmp}/_podlaz"
grep -F '__complete zsh' "${tmp}/_podlaz"
grep -F 'complete -c podlaz -f' "${tmp}/podlaz.fish"
grep -F 'complete -c plz -f' "${tmp}/podlaz.fish"
grep -F '__complete fish' "${tmp}/podlaz.fish"

top_level="$(go run ./cmd/podlaz __complete bash 1 podlaz "")"
grep -F $'connect\tConnect full VPN' <<<"${top_level}"
grep -F $'debug\tAdvanced diagnostics' <<<"${top_level}"
! grep -Eq '^(plan|check|doctor|logs|recover)([[:space:]]|$)' <<<"${top_level}"

debug="$(go run ./cmd/podlaz __complete bash 2 podlaz debug "")"
grep -F $'doctor\tRun diagnostics' <<<"${debug}"
grep -F $'logs\tShow logs' <<<"${debug}"
grep -F $'proxy\tConnect with Proxy-only protection' <<<"${debug}"
grep -F $'recover\tInspect or execute exact-owned recovery' <<<"${debug}"

connect_flags="$(go run ./cmd/podlaz __complete bash 2 podlaz connect --)"
! grep -Eq -- '--(mode|handoff|json|plain|verbose)' <<<"${connect_flags}"

recover_flags="$(go run ./cmd/podlaz __complete bash 3 podlaz debug recover --)"
grep -F -- '--execute' <<<"${recover_flags}"
! grep -F -- '--yes' <<<"${recover_flags}"
