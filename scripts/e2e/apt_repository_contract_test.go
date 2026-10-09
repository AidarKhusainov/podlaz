package e2e_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func readAPTContractFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestSignedAPTRepositoryBuilderUsesExactPackagesAndSignedMetadata(t *testing.T) {
	builder := readAPTContractFile(t, "../ci/build-apt-repository.sh")
	for _, required := range []string{
		"apt-ftparchive packages pool/main/p/podlaz",
		"APT::FTPArchive::Release::Suite=stable",
		"APT::FTPArchive::Release::Components=main",
		"apt-ftparchive --arch",
		"binary-${architecture}",
		"dists/stable/InRelease",
		"dists/stable/Release.gpg",
		"gpgv --keyring",
		"podlaz-archive-keyring.gpg",
		"podlaz-archive-keyring.fingerprint",
		"cmp -s --",
		"package index lost exact checksum provenance",
		"PODLAZ_APT_TEST_FAIL_STAGE",
		"after-metadata",
		"after-signing",
		"unsupported Debian architecture",
	} {
		if !strings.Contains(builder, required) {
			t.Fatalf("signed APT repository builder is missing %q", required)
		}
	}
	for _, forbidden := range []string{"apt-key", "go build", "nfpm", "scripts/build-deb.sh"} {
		if strings.Contains(builder, forbidden) {
			t.Fatalf("signed APT repository builder must not rebuild packages or use legacy trust command %q", forbidden)
		}
	}
}

func TestSignedAPTRepositoryQualificationDoesNotMutateRunnerNetworking(t *testing.T) {
	scenario := readAPTContractFile(t, "hosted-apt-repository.sh")
	for _, required := range []string{
		"--private-network",
		"file:/opt/podlaz-apt-site/apt",
		"Signed-By: /etc/apt/keyrings/podlaz-archive-keyring.gpg",
		"apt-get update",
		"apt-get install -y",
		"apt-get upgrade -y",
		"systemctl is-active --quiet podlazd.service",
		"assert_exact_podlaz_package_runtime_provenance",
		"missing-key-site",
		"wrong-key-site",
		"after-metadata",
		"after-signing",
		"rotation-site",
		"repository.rerun",
		"repository.failure_atomicity",
		"repository.rotation_boundary",
		"runner.network_isolation",
	} {
		if !strings.Contains(scenario, required) {
			t.Fatalf("signed APT repository qualification is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"--network-veth",
		"--network-veth-extra",
		"iptables ",
		"ip link ",
		"ip route ",
		"ip rule ",
		"nft add ",
		"/proc/sys/net/",
		"nmcli ",
		"resolvectl ",
	} {
		if strings.Contains(scenario, forbidden) {
			t.Fatalf("APT qualification must not mutate ordinary runner networking; found %q", forbidden)
		}
	}
}

func TestSignedAPTRepositoryProductionPublicationIsGuardedAndAtomic(t *testing.T) {
	workflow := readAPTContractFile(t, "../../.github/workflows/apt-repository.yml")
	release := readAPTContractFile(t, "../../.github/workflows/release.yml")

	for _, required := range []string{
		"workflow_call:",
		"workflow_dispatch:",
		"PODLAZ_APT_PUBLISH_ENABLED == 'true'",
		"environment: apt-production",
		"PODLAZ_APT_SIGNING_PRIVATE_KEY",
		"PODLAZ_APT_SIGNING_PASSPHRASE",
		"PODLAZ_APT_SIGNING_FINGERPRINT",
		"actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c",
		"bash scripts/ci/verify-release-artifacts.sh",
		"gh attestation verify",
		"bash scripts/e2e/hosted-apt-repository.sh",
		"actions/upload-pages-artifact@fc324d3547104276b827a68afc52ff2a11cc49c9",
		"actions/deploy-pages@368f82528645a54fb793d4d04e342629a3f51346",
		"needs:\n      - resolve\n      - qualify",
		"cancel-in-progress: false",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("production APT publication workflow is missing %q", required)
		}
	}
	for _, forbidden := range []string{"scripts/build-deb.sh", "nfpm package", "apt-key"} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("production APT publication must consume exact packages and scoped trust; found %q", forbidden)
		}
	}

	for _, required := range []string{
		"  apt-repository:\n",
		"      - attest-and-publish\n",
		"    uses: ./.github/workflows/apt-repository.yml\n",
		"      release_manifest_sha256: ${{ needs.build.outputs.release_manifest_sha256 }}\n",
	} {
		if !strings.Contains(release, required) {
			t.Fatalf("release workflow lost signed APT promotion dependency %q", required)
		}
	}

	for _, action := range []string{
		"actions/checkout",
		"actions/download-artifact",
		"actions/upload-artifact",
		"actions/upload-pages-artifact",
		"actions/deploy-pages",
	} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + "[0-9a-f]{40}")
		if !re.MatchString(workflow) {
			t.Fatalf("%s must be pinned to an immutable commit", action)
		}
	}
}

func TestSignedAPTRepositoryHostedWorkflowUsesOnlyEphemeralSigningAuthority(t *testing.T) {
	workflow := readAPTContractFile(t, "../../.github/workflows/hosted-apt-repository.yml")
	for _, required := range []string{
		"Create ephemeral qualification signing key",
		"apt-ci@example.invalid",
		"PODLAZ_APT_SIGNING_KEY_FILE:",
		"PODLAZ_APT_SIGNING_FINGERPRINT:",
		"CANDIDATE_ARM64_DEB",
		"gcc-aarch64-linux-gnu",
		"bash scripts/e2e/hosted-apt-repository.sh",
		"Remove private signing and guest state",
		"podlaz-hosted-apt-repository",
	} {
		if !strings.Contains(workflow, required) {
			t.Fatalf("hosted APT qualification workflow is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"secrets.PODLAZ_APT_SIGNING_PRIVATE_KEY",
		"secrets.PODLAZ_APT_SIGNING_PASSPHRASE",
		"environment: apt-production",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Fatalf("pull-request APT qualification must remain secret-free; found %q", forbidden)
		}
	}
}
