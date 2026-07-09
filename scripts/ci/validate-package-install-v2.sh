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
export DEBIAN_FRONTEND=noninteractive

test -f "${package}"
test "$(dpkg-deb --field "${package}" Architecture)" = amd64

sudo -E apt-get update
sudo -E apt-get install -y --no-install-recommends iproute2 nftables polkitd || \
  sudo -E apt-get install -y --no-install-recommends iproute2 nftables policykit-1
sudo -E apt install -y "./${package}"

for binary in podlaz plz; do
  "${binary}" version | tee "/tmp/${binary}-version.txt"
  grep -Fx "podlaz version ${version}" "/tmp/${binary}-version.txt"
  grep -Fx "commit: ${commit}" "/tmp/${binary}-version.txt"
  grep -Fx "built: ${built}" "/tmp/${binary}-version.txt"
done

test -x /usr/bin/podlaz
test -x /usr/bin/plz
test -x /usr/bin/podlazd
test -x /usr/lib/podlaz/xray
test -s /usr/share/doc/podlaz/third-party/xray-LICENSE
test -f /usr/lib/systemd/system/podlazd.service
grep -Fx 'Environment=PODLAZ_SERVICE=systemd' /usr/lib/systemd/system/podlazd.service
grep -Fx 'Environment=PODLAZ_POLKIT_AUTHORIZATION=required' /usr/lib/systemd/system/podlazd.service
grep -Fx 'Environment=PODLAZ_XRAY_PATH=/usr/lib/podlaz/xray' /usr/lib/systemd/system/podlazd.service
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

sudo -E apt install -y --reinstall "./${package}"
podlaz version | tee /tmp/podlaz-version-reinstall.txt
grep -Fx "podlaz version ${version}" /tmp/podlaz-version-reinstall.txt
grep -Fx "commit: ${commit}" /tmp/podlaz-version-reinstall.txt
grep -Fx "built: ${built}" /tmp/podlaz-version-reinstall.txt
test -x /usr/lib/podlaz/xray

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
test ! -e /usr/lib/podlaz/xray
test ! -e /usr/lib/systemd/system/podlazd.service
test ! -e /usr/lib/sysusers.d/podlaz.conf
