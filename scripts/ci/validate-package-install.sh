#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ]; then
  echo "usage: $0 <amd64-package.deb>" >&2
  exit 2
fi

package="$1"
version="${PODLAZ_EXPECT_VERSION:-0.0.0~dev}"
commit="${PODLAZ_EXPECT_COMMIT:-ci-test}"
built="${PODLAZ_EXPECT_BUILT:-Jun 19 2026}"
validate_service="${PODLAZ_VALIDATE_SERVICE:-0}"

test -f "${package}"
test "$(dpkg-deb --field "${package}" Architecture)" = amd64

ip route show > /tmp/podlaz-routes-before.txt
sudo apt install -y "./${package}"
ip route show > /tmp/podlaz-routes-after.txt
diff -u /tmp/podlaz-routes-before.txt /tmp/podlaz-routes-after.txt

dpkg -L podlaz
podlaz version | tee /tmp/podlaz-version.txt
grep -Fx "podlaz version ${version}" /tmp/podlaz-version.txt
grep -Fx "commit: ${commit}" /tmp/podlaz-version.txt
grep -Fx "built: ${built}" /tmp/podlaz-version.txt
plz version | tee /tmp/plz-version.txt
grep -Fx "podlaz version ${version}" /tmp/plz-version.txt
grep -Fx "commit: ${commit}" /tmp/plz-version.txt
grep -Fx "built: ${built}" /tmp/plz-version.txt

test -x /usr/bin/podlaz
test -x /usr/bin/plz
test -x /usr/bin/podlazd
test -f /usr/lib/systemd/system/podlazd.service
test -f /usr/lib/sysusers.d/podlaz.conf
test -f /usr/share/bash-completion/completions/podlaz
test -f /usr/share/bash-completion/completions/plz
test -f /usr/share/zsh/vendor-completions/_podlaz
test -f /usr/share/zsh/vendor-completions/_plz
test -f /usr/share/fish/vendor_completions.d/podlaz.fish
test -f /usr/share/fish/vendor_completions.d/plz.fish
test -f /usr/share/polkit-1/actions/io.github.aidarkhusainov.podlaz.policy
test -f /usr/share/man/man1/podlaz.1.gz
test -f /usr/share/man/man8/podlazd.8.gz
test ! -e /usr/share/metainfo/io.github.aidarkhusainov.podlaz.metainfo.xml
man -l /usr/share/man/man1/podlaz.1.gz >/dev/null
man -l /usr/share/man/man8/podlazd.8.gz >/dev/null
bash -n /usr/share/bash-completion/completions/podlaz
bash -n /usr/share/bash-completion/completions/plz
zsh -n /usr/share/zsh/vendor-completions/_podlaz
zsh -n /usr/share/zsh/vendor-completions/_plz
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/podlaz.fish'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/plz.fish'
bash --noprofile --norc -c 'source /usr/share/bash-completion/completions/podlaz; COMP_WORDS=(podlaz ""); COMP_CWORD=1; _podlaz; printf "%s\n" "${COMPREPLY[@]}" | grep -Fx completion'
bash --noprofile --norc -c 'source /usr/share/bash-completion/completions/plz; COMP_WORDS=(plz ""); COMP_CWORD=1; _podlaz; printf "%s\n" "${COMPREPLY[@]}" | grep -Fx completion'
zsh -fc 'autoload -Uz compinit; fpath=(/usr/share/zsh/vendor-completions $fpath); compinit -D; autoload -Uz _podlaz; whence _podlaz >/dev/null'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/podlaz.fish; complete -C "podlaz " | grep -F completion'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/podlaz.fish; complete -C "podlaz plan -" | grep -F -- "--mode"'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/podlaz.fish; complete -C "podlaz plan --mode " | grep -F "proxy-only"'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/podlaz.fish; complete -C "podlaz profile add --protocol " | grep -F "vless"'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/podlaz.fish; complete -C "podlaz logs -" | grep -F -- "--follow"'
fish --no-config --command 'source /usr/share/fish/vendor_completions.d/plz.fish; complete -C "plz plan -" | grep -F -- "--mode"'

if [ "${validate_service}" = 1 ]; then
  sudo systemctl daemon-reload
  sudo systemctl is-enabled --quiet podlazd.service

  active_attempts=0
  while ! sudo systemctl is-active --quiet podlazd.service; do
    active_attempts=$((active_attempts + 1))
    if [ "${active_attempts}" -ge 50 ]; then
      break
    fi
    sleep 0.2
  done
  sudo systemctl is-active --quiet podlazd.service

  socket_attempts=0
  while [ ! -S /run/podlaz/podlazd.sock ]; do
    socket_attempts=$((socket_attempts + 1))
    if [ "${socket_attempts}" -ge 50 ]; then
      break
    fi
    sleep 0.2
  done
  test -S /run/podlaz/podlazd.sock
fi

sudo apt install -y --reinstall "./${package}"
podlaz version | tee /tmp/podlaz-version-reinstall.txt
grep -Fx "podlaz version ${version}" /tmp/podlaz-version-reinstall.txt
grep -Fx "commit: ${commit}" /tmp/podlaz-version-reinstall.txt
grep -Fx "built: ${built}" /tmp/podlaz-version-reinstall.txt

test -f /usr/share/bash-completion/completions/podlaz
test -f /usr/share/bash-completion/completions/plz
test -f /usr/share/zsh/vendor-completions/_podlaz
test -f /usr/share/zsh/vendor-completions/_plz
test -f /usr/share/fish/vendor_completions.d/podlaz.fish
test -f /usr/share/fish/vendor_completions.d/plz.fish
test -f /usr/share/man/man1/podlaz.1.gz
test -f /usr/share/man/man8/podlazd.8.gz

sudo systemctl stop podlazd.service >/dev/null 2>&1 || true
sudo apt purge -y podlaz >/dev/null 2>&1 || true
if command -v deb-systemd-helper >/dev/null 2>&1; then
  sudo deb-systemd-helper purge podlazd.service >/dev/null 2>&1 || true
fi
sudo systemctl daemon-reload >/dev/null 2>&1 || true
sudo systemctl reset-failed podlazd.service >/dev/null 2>&1 || true

if dpkg -L podlaz; then
  echo "podlaz package still has installed files after purge" >&2
  exit 1
fi
test ! -e /usr/bin/podlaz
test ! -e /usr/bin/plz
test ! -e /usr/bin/podlazd
test ! -e /usr/lib/systemd/system/podlazd.service
test ! -e /usr/lib/sysusers.d/podlaz.conf
