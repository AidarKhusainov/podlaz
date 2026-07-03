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
grep -F '#compdef podlaz' "${tmp}/_podlaz"
grep -F '__complete zsh' "${tmp}/_podlaz"
grep -F 'complete -c podlaz -f' "${tmp}/podlaz.fish"
grep -F 'complete -c plz -f' "${tmp}/podlaz.fish"
grep -F '__complete fish' "${tmp}/podlaz.fish"
grep -F -- "-l mode -x -a 'proxy-only tun'" "${tmp}/podlaz.fish"
grep -F -- "-l protocol -x -a 'vless vmess trojan shadowsocks'" "${tmp}/podlaz.fish"
fish --no-config --command "source ${tmp}/podlaz.fish; complete -C 'podlaz plan -' | grep -F -- '--mode'"
fish --no-config --command "source ${tmp}/podlaz.fish; complete -C 'podlaz plan --mode ' | grep -F 'proxy-only'"
fish --no-config --command "source ${tmp}/podlaz.fish; complete -C 'podlaz profile add --protocol ' | grep -F 'vless'"
fish --no-config --command "source ${tmp}/podlaz.fish; complete -C 'podlaz logs -' | grep -F -- '--follow'"
fish --no-config --command "source ${tmp}/podlaz.fish; complete -C 'plz plan -' | grep -F -- '--mode'"
