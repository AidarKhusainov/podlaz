package e2e_test

import (
	"os"
	"strings"
	"testing"
)

func TestPublicAPTVerificationPreservesScopedTrust(t *testing.T) {
	data, err := os.ReadFile("../ci/public-apt-smoke.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(data)
	for _, required := range []string{
		"--network bridge", "ubuntu:24.04", "EXPECTED_FINGERPRINT",
		"Signed-By: /etc/apt/keyrings/podlaz.gpg", "apt-get update",
		"apt-cache policy podlaz", "apt-get download", "dpkg-deb -x", "podlaz version",
		"package-root/usr/lib/systemd/system/podlazd.service", "https://aidarkhusainov.github.io/podlaz/apt",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("missing public verification assertion: %q", required)
		}
	}
	for _, forbidden := range []string{"apt-key", "Trusted: yes", "--privileged", "--network host", "systemctl start", `apt-get install -y --no-install-recommends "podlaz=`} {
		if strings.Contains(script, forbidden) {
			t.Errorf("unsafe public verifier operation: %q", forbidden)
		}
	}
}

func TestPublicAPTSmokeRunsAfterDeploymentAndDaily(t *testing.T) {
	production, err := os.ReadFile("../../.github/workflows/apt-repository.yml")
	if err != nil {
		t.Fatal(err)
	}
	daily, err := os.ReadFile("../../.github/workflows/public-apt-availability.yml")
	if err != nil {
		t.Fatal(err)
	}
	p, d := string(production), string(daily)
	for _, required := range []string{
		"  verify-public:", "      - deploy", "environment: apt-production",
		"PODLAZ_APT_SIGNING_FINGERPRINT", "bash scripts/ci/public-apt-smoke.sh",
	} {
		if !strings.Contains(p, required) {
			t.Errorf("production missing: %q", required)
		}
	}
	for _, required := range []string{
		"  schedule:", "  workflow_dispatch:", "environment: apt-production",
		"PODLAZ_APT_SIGNING_FINGERPRINT", "bash scripts/ci/public-apt-smoke.sh",
		`version="${version#v}"`,
		`[[ ! "${version}" =~ ^[0-9]+[.][0-9]+[.][0-9]+$ ]]`,
	} {
		if !strings.Contains(d, required) {
			t.Errorf("daily verification missing: %q", required)
		}
	}
}
