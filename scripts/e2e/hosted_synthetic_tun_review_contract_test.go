package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const hostedSyntheticActiveAuthorityHelper = "hosted_synthetic_active_authority.py"

func TestHostedSyntheticTUNReportValidationIsFailClosed(t *testing.T) {
	keys := []string{
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
	}

	run := func(overrides map[string]string) error {
		t.Helper()
		root := t.TempDir()
		var report strings.Builder
		for _, key := range keys {
			state := "pass"
			if override, ok := overrides[key]; ok {
				state = override
			}
			report.WriteString(key + "=" + state + "\n")
		}
		report.WriteString("failure.class=none\n")
		report.WriteString("failure.step=none\n")
		if err := os.WriteFile(filepath.Join(root, "hosted-synthetic-tun.txt"), []byte(report.String()), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("bash", hostedSyntheticTUNScript, "validate-report")
		cmd.Env = append(os.Environ(), "E2E_ARTIFACT_DIR="+root, "E2E_TMP_ROOT="+filepath.Join(root, "private"))
		return cmd.Run()
	}

	if err := run(nil); err != nil {
		t.Fatalf("all-pass required report rejected: %v", err)
	}
	if err := run(map[string]string{"tun.doctor": "observed"}); err != nil {
		t.Fatalf("topology-dependent doctor observation rejected: %v", err)
	}
	for name, overrides := range map[string]map[string]string{
		"required observed":   {"candidate.provenance": "observed"},
		"required unavailable": {"tun.system_dns": "unavailable"},
		"doctor unavailable":   {"tun.doctor": "unavailable"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(overrides); err == nil {
				t.Fatal("report validator accepted a non-success required evidence state")
			}
		})
	}
}

func TestHostedSyntheticTUNVerifiedActiveUsesExactPersistedAuthority(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	start := strings.Index(script, "assert_verified_active_authority() {")
	end := strings.Index(script, "\nrun_active_traffic_checks() {")
	if start < 0 || end <= start {
		t.Fatal("verified-active authority function boundaries not found")
	}
	active := script[start:end]
	requireHostedSyntheticTUNMarkers(t, active,
		"${ACTIVE_AUTHORITY_HELPER}",
		"${GUEST_PRIVATE}/status.json",
		"/run/podlaz/transactions",
		"/run/podlaz/network-session-continuation.json",
		"/proc/sys/kernel/random/boot_id",
		"/run/podlaz/generated/xray.json",
		"resolved-dns.txt",
		"resolved-domain.txt",
		"resolved-default-route.txt",
		"nft-ruleset.json",
	)
	forbidHostedSyntheticTUNMarkers(t, active,
		"resolvectl status podlaz0",
		"nft list table inet podlaz",
		"nft list tables | grep -E 'table inet podlaz_pe_",
		"test -s /run/podlaz/network-session-continuation.json",
		"find /run/podlaz/transactions -mindepth 1 -maxdepth 1 -type f -name \"*.json\"",
		"test -s /run/podlaz/generated/xray.json",
	)
}

func TestHostedSyntheticTUNActiveAuthorityVerifierRejectsBootDNSAndEnvelopeDrift(t *testing.T) {
	root := t.TempDir()
	transactions := filepath.Join(root, "transactions")
	if err := os.Mkdir(transactions, 0o700); err != nil {
		t.Fatal(err)
	}
	status := filepath.Join(root, "status.json")
	session := filepath.Join(root, "session.json")
	bootID := filepath.Join(root, "boot-id")
	runtimeConfig := filepath.Join(root, "xray.json")
	resolvedDNS := filepath.Join(root, "resolved-dns.txt")
	resolvedDomain := filepath.Join(root, "resolved-domain.txt")
	resolvedDefault := filepath.Join(root, "resolved-default-route.txt")
	nftRuleset := filepath.Join(root, "nft.json")

	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(status, `{"connection":"active","mode":"tun","active_transaction_id":"tx-1","tun_health":{"state":"verified"},"transactions":[{"id":"tx-1","state":"committed","requires_cleanup":false}]}`)
	write(filepath.Join(transactions, "tx-1.json"), `{
  "schema_version":"podlaz.transaction.v1","owner":"podlaz","id":"tx-1","profile_id":"profile-1","mode":"tun","state":"committed",
  "desired_plan":{
    "tun":{"interface_name":"podlaz0","mtu":1500,"owner":"xray:tun-inbound"},
    "dns":{"backend":"systemd-resolved per-link DNS","link":"podlaz0","servers":["1.1.1.1"],"search_domains":["~."],"owner":"podlaz"},
    "nftables":{"family":"inet","table":"podlaz","owner":"podlaz:firewall","chains":[{"name":"output","hook":"output","type":"filter","priority":0,"policy":"accept","owner":"podlaz:firewall","rules":["ip daddr 172.31.253.1 accept owner podlaz:firewall:server-bypass","oifname \\"lo\\" accept owner podlaz:firewall:loopback","oifname \\"podlaz0\\" accept owner podlaz:firewall:tun-egress","oifname != \\"podlaz0\\" reject owner podlaz:firewall:kill-switch"]}]},
    "core":{"runtime_config_path":"/run/podlaz/generated/xray.json","process_label":"xray","owner":"podlaz"}
  },
  "rollback":{
    "dns":[{"backend":"systemd-resolved per-link DNS","link":"podlaz0","search_domains":["~."],"owner":"podlaz:dns-link"}],
    "nftables":[{"family":"inet","table":"podlaz","owner":"podlaz:firewall"}],
    "generated_configs":[{"path":"/run/podlaz/generated/xray.json","owner":"podlaz"}],
    "child_processes":[{"pid":123,"pid_file":"/run/podlaz/xray.pid","label":"xray","config_ref":"/run/podlaz/generated/xray.json","start_time":"1","owner":"podlaz"}]
  }
}`)
	write(session, `{"schema_version":"podlaz.network-session-state.v1","owner":"podlaz","boot_id":"boot-1","session_id":"0123456789abcdef0123456789abcdef","intent":"resume","request":{"mode":"tun","profile":{"id":"profile-1"}},"protection":{"state":"armed","composition_version":1,"family":"inet","table":"podlaz_pe_0123456789ab","tun_interface":"podlaz0","bootstrap_ipv4":["172.31.253.1"]}}`)
	write(bootID, "boot-1\n")
	write(runtimeConfig, "{}\n")
	write(resolvedDNS, "Global:\nLink 2 (host0): 1.0.0.1\nLink 3 (podlaz0): 1.1.1.1\n")
	write(resolvedDomain, "Global:\nLink 2 (host0):\nLink 3 (podlaz0): ~.\n")
	write(resolvedDefault, "Global: no\nLink 2 (host0): yes\nLink 3 (podlaz0): yes\n")
	write(nftRuleset, exactHostedSyntheticNFTRuleset())

	run := func() error {
		t.Helper()
		cmd := exec.Command("python3", hostedSyntheticActiveAuthorityHelper,
			"--status", status,
			"--transactions", transactions,
			"--session", session,
			"--boot-id", bootID,
			"--runtime-config", runtimeConfig,
			"--resolved-dns", resolvedDNS,
			"--resolved-domain", resolvedDomain,
			"--resolved-default-route", resolvedDefault,
			"--nft-ruleset", nftRuleset,
		)
		return cmd.Run()
	}
	if err := run(); err != nil {
		t.Fatalf("exact active authority fixture rejected: %v", err)
	}

	write(bootID, "boot-2\n")
	if err := run(); err == nil {
		t.Fatal("active authority verifier accepted previous-boot Network Session authority")
	}
	write(bootID, "boot-1\n")

	write(resolvedDNS, "Global:\nLink 2 (host0): 1.0.0.1\nLink 3 (podlaz0): 9.9.9.9\n")
	if err := run(); err == nil {
		t.Fatal("active authority verifier accepted wrong resolved DNS composition")
	}
	write(resolvedDNS, "Global:\nLink 2 (host0): 1.0.0.1\nLink 3 (podlaz0): 1.1.1.1\n")

	badEnvelope := strings.Replace(exactHostedSyntheticNFTRuleset(), "podlaz:privacy-envelope:block-direct", "podlaz:privacy-envelope:foreign", 1)
	write(nftRuleset, badEnvelope)
	if err := run(); err == nil {
		t.Fatal("active authority verifier accepted wrong Privacy Envelope composition")
	}
}

func exactHostedSyntheticNFTRuleset() string {
	return `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz","handle":10}},
{"chain":{"family":"inet","table":"podlaz","name":"output","handle":11,"type":"filter","hook":"output","prio":0,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":12,"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"172.31.253.1"}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:firewall:server-bypass"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":13,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:firewall:loopback"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":14,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:firewall:tun-egress"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":15,"expr":[{"match":{"op":"!=","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":1,"bytes":2}},{"reject":null}],"comment":"podlaz:firewall:kill-switch"}},
{"table":{"family":"inet","name":"podlaz_pe_0123456789ab","handle":20}},
{"chain":{"family":"inet","table":"podlaz_pe_0123456789ab","name":"output","handle":21,"type":"filter","hook":"output","prio":-10,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":22,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"lo"}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:privacy-envelope:loopback"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":23,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:privacy-envelope:tun-egress"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":24,"expr":[{"match":{"op":"==","left":{"payload":{"protocol":"ip","field":"daddr"}},"right":"172.31.253.1"}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:privacy-envelope:bootstrap"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":25,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv4"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"udp"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"sport"}},"right":68}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":67}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:privacy-envelope:dhcp4"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":26,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv6"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"udp"}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"sport"}},"right":546}},{"match":{"op":"==","left":{"payload":{"protocol":"udp","field":"dport"}},"right":547}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:privacy-envelope:dhcp6"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":27,"expr":[{"match":{"op":"==","left":{"meta":{"key":"nfproto"}},"right":"ipv6"}},{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"icmpv6"}},{"match":{"op":"==","left":{"payload":{"protocol":"icmpv6","field":"type"}},"right":{"set":["nd-router-solicit","nd-neighbor-solicit","nd-neighbor-advert"]}}},{"counter":{"packets":1,"bytes":2}},{"accept":null}],"comment":"podlaz:privacy-envelope:ipv6-link-control"}},
{"rule":{"family":"inet","table":"podlaz_pe_0123456789ab","chain":"output","handle":28,"expr":[{"counter":{"packets":1,"bytes":2}},{"reject":null}],"comment":"podlaz:privacy-envelope:block-direct"}}
]}`
}

func TestHostedSyntheticTUNDoctorUsesStructuredCanonicalSemantics(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	start := strings.Index(script, "run_tun_doctor() {")
	end := strings.Index(script, "\nassert_terminal_authority_clean() {")
	if start < 0 || end <= start {
		t.Fatal("doctor function boundaries not found")
	}
	doctor := script[start:end]
	requireHostedSyntheticTUNMarkers(t, doctor,
		"doctor --tun --json",
		"status",
		"healthy",
		"degraded",
	)
	forbidHostedSyntheticTUNMarkers(t, doctor,
		"3) record_evidence tun.doctor observed",
	)
}

func TestHostedSyntheticTUNFailureClassificationDoesNotPrejudgeProduct(t *testing.T) {
	script := readHostedSyntheticTUNFile(t, hostedSyntheticTUNScript)
	requireHostedSyntheticTUNMarkers(t, script,
		"diagnostic_unknown",
		"mark_failure diagnostic_unknown profile.import",
		"mark_failure diagnostic_unknown tun.connect",
		"mark_failure diagnostic_unknown tun.active_traffic",
		"mark_failure diagnostic_unknown tun.doctor",
		"mark_failure diagnostic_unknown guest.connectivity_restored",
	)
}

func TestHostedSyntheticTUNArchitectureDistinguishesProductMutationFromHostedPlumbing(t *testing.T) {
	architecture := readHostedSyntheticTUNFile(t, "../../ARCHITECTURE.md")
	requireHostedSyntheticTUNMarkers(t, architecture,
		"Podlaz-owned destructive networking never mutates the outer hosted runner",
		"infrastructure-owned guest plumbing",
		"Direct destructive product networking on a host remains dedicated-only",
	)
}
