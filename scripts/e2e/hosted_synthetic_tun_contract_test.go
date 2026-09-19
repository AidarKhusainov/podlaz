package e2e_test

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

const (
	hostedSyntheticTUNWorkflow = "../../.github/workflows/hosted-synthetic-tun.yml"
	hostedSyntheticTUNScript   = "hosted-synthetic-tun.sh"
)

func readHostedSyntheticTUNFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func requireHostedSyntheticTUNMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(text, marker) {
			t.Fatalf("hosted synthetic TUN contract lost %q", marker)
		}
	}
}

func forbidHostedSyntheticTUNMarkers(t *testing.T, text string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(text, marker) {
			t.Fatalf("hosted synthetic TUN contract contains forbidden %q", marker)
		}
	}
}

func TestHostedSyntheticTUNWorkflowIsThinPermanentQualification(t *testing.T) {
	workflow := readHostedSyntheticTUNFile(t, hostedSyntheticTUNWorkflow)

	requireHostedSyntheticTUNMarkers(t, workflow,
		"name: Hosted Synthetic TUN",
		"pull_request:",
		"workflow_dispatch:",
		"schedule:",
		"runs-on: ubuntu-24.04",
		"persist-credentials: false",
		"bash scripts/build-deb.sh",
		"bash scripts/e2e/hosted-synthetic-tun.sh",
		"hosted-synthetic-tun.txt",
	)

	for _, action := range []string{"actions/checkout", "actions/setup-go", "actions/upload-artifact"} {
		re := regexp.MustCompile(regexp.QuoteMeta("uses: "+action+"@") + `[0-9a-f]{40}`)
		if !re.MatchString(workflow) {
			t.Fatalf("%s must be pinned to an immutable 40-hex commit", action)
		}
	}

	forbidHostedSyntheticTUNMarkers(t, workflow,
		"self-hosted",
		"${{ secrets.",
		"PODLAZ_E2E_PROFILE_URI",
		"podlaz connect --mode tun",
		"machinectl ",
		"systemd-nspawn ",
		"iptables ",
		"nft ",
		"qemu-",
		"hosted-e2e-capability",
	)
}

func TestHostedSyntheticTUNCandidateProvenanceMatchesCheckedOutCandidate(t *testing.T) {
	workflow := readHostedSyntheticTUNFile(t, hostedSyntheticTUNWorkflow)
	requireHostedSyntheticTUNMarkers(t, workflow,
		"CANDIDATE_COMMIT: ${{ github.sha }}",
		"PODLAZ_COMMIT: ${{ env.CANDIDATE_COMMIT }}",
		"PODLAZ_E2E_CANDIDATE_COMMIT: ${{ env.CANDIDATE_COMMIT }}",
	)
	forbidHostedSyntheticTUNMarkers(t, workflow,
		"PODLAZ_COMMIT: ${{ github.sha }}",
	)
}

func TestHostedSyntheticTUNScenarioOwnsCanonicalLifecycle(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)

	requireHostedSyntheticTUNMarkers(t, script,
		"validate_candidate()",
		"capture_outer_baseline()",
		"prepare_system_guest()",
		"start_system_guest()",
		"install_candidate_in_guest()",
		"assert_guest_package_provenance()",
		"start_synthetic_xray_endpoint()",
		"install_tun_authorization()",
		"assert_ordinary_user_boundary()",
		"capture_guest_network_baseline()",
		"assert_verified_active_authority()",
		"run_active_traffic_checks()",
		"run_tun_doctor()",
		"assert_terminal_authority_clean()",
		"assert_guest_network_baseline_restored()",
		"assert_clean_recovery_json_file",
		"assert_public_artifact_privacy()",
		"cleanup_outer_plumbing()",
		"/dev/net/tun",
		"settings\": {\"clients\"",
	)

	forbidHostedSyntheticTUNMarkers(t, script,
		"probe_qemu_",
		"qemu-system",
		"capture_pid=",
		"argv_capture_pid=",
		"transport_capture_pid=",
		"connect_capture_pid=",
		"runuser -u e2e -g podlaz",
		"PODLAZ_E2E_PROFILE_URI",
	)
	cmd := exec.Command("bash", "-n", hostedSyntheticTUNScript)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bash -n %s: %v\n%s", hostedSyntheticTUNScript, err, output)
	}
}

func TestHostedSyntheticTUNVerifiedActiveChecksExactRouteRulePresence(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	activeStart := strings.Index(script, "assert_verified_active_authority() {")
	activeEnd := strings.Index(script, "\nrun_active_traffic_checks() {")
	if activeStart < 0 || activeEnd <= activeStart {
		t.Fatal("verified-active authority function boundaries not found")
	}
	active := script[activeStart:activeEnd]
	requireHostedSyntheticTUNMarkers(t, script,
		`HOSTED_NETWORK_AUTHORITY_HELPER="/workspace/scripts/e2e/hosted_synthetic_network_authority.py"`,
	)
	requireHostedSyntheticTUNMarkers(t, active,
		`"${HOSTED_NETWORK_AUTHORITY_HELPER}" snapshot`,
		`jq -e '(.routes | length) > 0 and (.rules | length) > 0'`,
		`"${HOSTED_NETWORK_AUTHORITY_HELPER}" verify-present`,
	)
	forbidHostedSyntheticTUNMarkers(t, active,
		`"${FALLBACK_NETWORK_HELPER}" snapshot`,
		`"${FALLBACK_NETWORK_HELPER}" verify-present`,
		" cleanup ",
	)
}

func TestHostedSyntheticTUNDynamicNetworkSnapshotRequiresExactTransactionProof(t *testing.T) {
	tmp := t.TempDir()
	txDir := tmp + "/transactions"
	if err := os.Mkdir(txDir, 0o700); err != nil {
		t.Fatal(err)
	}
	transaction := `{
  "schema_version":"podlaz.transaction.v1",
  "owner":"podlaz",
  "id":"dynamic-authority",
  "mode":"tun",
  "state":"committed",
  "desired_plan":{
    "routes":[
      {"kind":"route","table":"51821","cidr":"default","via":"","dev":"podlaz0","owner":"podlaz:route","operation":"add"},
      {"kind":"route","table":"main","cidr":"203.0.113.10/32","via":"192.0.2.1","dev":"host0","owner":"podlaz:route","operation":"add"}
    ],
    "steps":[
      {"kind":"policy-rule","target":"priority 9997 to 203.0.113.10/32 lookup main","owner":"podlaz:policy-rule"},
      {"kind":"policy-rule","target":"priority 9998 from all lookup 51821","owner":"podlaz:policy-rule"}
    ]
  },
  "applied_steps":[
    {"kind":"route","target":"51821 default","owner":"podlaz:route"},
    {"kind":"route","target":"main 203.0.113.10/32","owner":"podlaz:route"},
    {"kind":"policy-rule","target":"priority 9997 to 203.0.113.10/32 lookup main","owner":"podlaz:policy-rule"},
    {"kind":"policy-rule","target":"priority 9998 from all lookup 51821","owner":"podlaz:policy-rule"}
  ],
  "rollback":{
    "routes":[
      {"table":"51821","cidr":"default","via":"","dev":"podlaz0","owner":"podlaz:route"},
      {"table":"main","cidr":"203.0.113.10/32","via":"192.0.2.1","dev":"host0","owner":"podlaz:route"}
    ],
    "policy_rules":[
      {"priority":9997,"to":"203.0.113.10/32","table":"main","owner":"podlaz:policy-rule"},
      {"priority":9998,"from":"all","table":"51821","owner":"podlaz:policy-rule"}
    ]
  }
}`
	if err := os.WriteFile(txDir+"/dynamic-authority.json", []byte(transaction), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := tmp + "/manifest.json"
	cmd := exec.Command("python3", "hosted_synthetic_network_authority.py", "snapshot", txDir, manifest)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("snapshot dynamic transaction: %v\n%s", err, output)
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, marker := range []string{`"table": "51821"`, `"priority": 9997`, `"priority": 9998`} {
		if !strings.Contains(text, marker) {
			t.Fatalf("dynamic authority manifest lost %s: %s", marker, text)
		}
	}

	broken := strings.Replace(transaction, `"target":"priority 9998 from all lookup 51821"`, `"target":"priority 9998 from all lookup 51822"`, 1)
	if err := os.WriteFile(txDir+"/dynamic-authority.json", []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("python3", "hosted_synthetic_network_authority.py", "snapshot", txDir, manifest).Run(); err == nil {
		t.Fatal("snapshot accepted applied policy rule outside exact desired transaction proof")
	}
}

func TestHostedSyntheticTUNDynamicNetworkManifestCanBeVerifiedPresentAndAbsent(t *testing.T) {
	tmp := t.TempDir()
	fakeIP := tmp + "/ip"
	fakeIPScript := `#!/bin/sh
case "$*" in
  "-4 rule show priority 9997")
    if [ "${FAKE_IP_MODE:-present}" = "present" ]; then
      printf '%s\n' '9997: from all to 203.0.113.10 lookup main'
    fi
    ;;
  "-4 rule show priority 9998")
    if [ "${FAKE_IP_MODE:-present}" = "present" ]; then
      printf '%s\n' '9998: from all lookup 51821'
    fi
    ;;
  "-4 route show table main exact 203.0.113.10/32")
    if [ "${FAKE_IP_MODE:-present}" = "present" ]; then
      printf '%s\n' '203.0.113.10 via 192.0.2.1 dev host0'
    fi
    ;;
  "-4 route show table 51821 exact default")
    if [ "${FAKE_IP_MODE:-present}" = "present" ]; then
      printf '%s\n' 'default dev podlaz0'
    fi
    ;;
  *) exit 64 ;;
esac
`
	if err := os.WriteFile(fakeIP, []byte(fakeIPScript), 0o755); err != nil {
		t.Fatalf("write fake ip: %v", err)
	}
	manifest := tmp + "/manifest.json"
	manifestJSON := `{"schema_version":"podlaz.e2e.hosted-network-authority.v1","routes":[{"family":"ipv4","table":"51821","cidr":"default","via":"","dev":"podlaz0"},{"family":"ipv4","table":"main","cidr":"203.0.113.10/32","via":"192.0.2.1","dev":"host0"}],"rules":[{"family":"ipv4","priority":9997,"source":"","destination":"203.0.113.10/32","mark":"","table":"main"},{"family":"ipv4","priority":9998,"source":"all","destination":"","mark":"","table":"51821"}]}`
	if err := os.WriteFile(manifest, []byte(manifestJSON), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	run := func(mode, command string) error {
		t.Helper()
		cmd := exec.Command("python3", "hosted_synthetic_network_authority.py", command, manifest)
		cmd.Env = append(os.Environ(), "PATH="+tmp+":"+os.Getenv("PATH"), "FAKE_IP_MODE="+mode)
		return cmd.Run()
	}
	if err := run("present", "verify-present"); err != nil {
		t.Fatalf("dynamic verify-present failed: %v", err)
	}
	if err := run("absent", "verify-absent"); err != nil {
		t.Fatalf("dynamic verify-absent failed: %v", err)
	}
	if err := run("present", "verify-absent"); err == nil {
		t.Fatal("verify-absent accepted present dynamic tuples")
	}
	if err := run("absent", "verify-present"); err == nil {
		t.Fatal("verify-present accepted absent dynamic tuples")
	}
}

func TestHostedSyntheticTUNNetworkManagerObservationIsTerminalPostcondition(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	activeStart := strings.Index(script, "assert_verified_active_authority() {")
	activeEnd := strings.Index(script, "\nrun_active_traffic_checks() {")
	terminalStart := strings.Index(script, "assert_terminal_authority_clean() {")
	terminalEnd := strings.Index(script, "\nrun_clean_recovery() {")
	if activeStart < 0 || activeEnd <= activeStart || terminalStart < 0 || terminalEnd <= terminalStart {
		t.Fatal("authority function boundaries not found")
	}
	active := script[activeStart:activeEnd]
	terminal := script[terminalStart:terminalEnd]
	marker := `nmcli -t -f NAME,DEVICE connection show --active | grep -F ':podlaz0'`
	forbidHostedSyntheticTUNMarkers(t, active, marker)
	requireHostedSyntheticTUNMarkers(t, terminal, marker)
}

func TestHostedSyntheticTUNEndpointIsRoutedBehindGuestGateway(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	requireHostedSyntheticTUNMarkers(t, script,
		`HOST_ENDPOINT_DEV="pzsyntsrv"`,
		`ENDPOINT_CIDR="172.31.253.1/32"`,
		`ENDPOINT_IP="172.31.253.1"`,
		`ip link add dev "${HOST_ENDPOINT_DEV}" type dummy`,
		`ip addr add "${ENDPOINT_CIDR}" dev "${HOST_ENDPOINT_DEV}"`,
		`ip link del dev "${HOST_ENDPOINT_DEV}"`,
		`"${ENDPOINT_IP}:${port}"`,
	)
	forbidHostedSyntheticTUNMarkers(t, script,
		`"${HOST_IP}:${port}"`,
		`@${HOST_IP}:`,
	)
}

func TestHostedSyntheticTUNEvidenceSchemaIsBounded(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	for _, key := range []string{
		"candidate.provenance",
		"ordinary_user.boundary",
		"tun.verified_active",
		"tun.system_dns",
		"tun.https_tls",
		"tun.doctor",
		"tun.clean_disconnect",
		"tun.terminal_cleanup",
		"tun.recovery_clean",
		"guest.baseline_restored",
		"outer.cleanup",
		"artifact.privacy",
	} {
		if !strings.Contains(script, key) {
			t.Fatalf("hosted synthetic TUN evidence schema lost %q", key)
		}
	}
	forbidHostedSyntheticTUNMarkers(t, script,
		"kernel.netns",
		"qemu.kvm",
		"qemu.tcg",
		"qemu.reboot",
	)
}

func TestHostedSyntheticTUNPolkitFixtureIsNarrow(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	start := strings.Index(script, "install_tun_authorization() {")
	end := strings.Index(script, "\nrun_guest_user() {")
	if start < 0 || end <= start {
		t.Fatal("TUN authorization function boundaries not found")
	}
	rule := script[start:end]
	requireHostedSyntheticTUNMarkers(t, rule,
		"io.github.aidarkhusainov.podlaz.connect-tun",
		"io.github.aidarkhusainov.podlaz.disconnect",
	)
	forbidHostedSyntheticTUNMarkers(t, rule,
		"io.github.aidarkhusainov.podlaz.connect-proxy-only",
		"action.id.indexOf(\"io.github.aidarkhusainov.podlaz.\")",
	)
}
