package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestPrivacyEnvelopeRealNFTRoundTrip(t *testing.T) {
	if os.Getenv("PODLAZ_TEST_REAL_NFT") != "1" {
		t.Skip("set PODLAZ_TEST_REAL_NFT=1 to run the privileged real-nftables contract")
	}
	if os.Geteuid() != 0 {
		t.Fatal("PODLAZ_TEST_REAL_NFT=1 requires root/CAP_NET_ADMIN for generation-guarded nftables mutation")
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Fatalf("nft command is required for real-nftables contract: %v", err)
	}

	foreignTable := "foreign_guard_" + realNFTUniqueSuffix(t)
	createRealNFTForeignSentinel(t, foreignTable)
	foreignBefore := realNFTTableJSON(t, foreignTable)
	t.Cleanup(func() {
		_ = exec.Command("nft", "delete", "table", "inet", foreignTable).Run()
	})

	plan := productionShapedPrivacyEnvelopePlanForTest()
	plan.Table = realNFTPrivacyEnvelopeTable(t)
	// Current composition-v1 generation uses the minimal ICMPv6 expression;
	// nft may canonicalize it further, which Verify must still recognize through
	// the structured semantic model rather than text equality.
	plan.Rules = append([]planner.TunFirewallRulePlan(nil), plan.Rules...)
	plan.Rules[5].Expr = "icmpv6 type { nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert }"

	ctx := context.Background()
	executor := PrivacyEnvelopeExecutor{}
	t.Cleanup(func() {
		// Emergency test isolation only. The assertions below still require the
		// production generation-guarded Remove path to succeed first.
		_ = exec.Command("nft", "delete", "table", plan.Family, plan.Table).Run()
	})

	present, err := executor.Exists(ctx, plan)
	if err != nil {
		t.Fatalf("observe fresh real Privacy Envelope identity: %v", err)
	}
	if present {
		t.Fatalf("random real Privacy Envelope table unexpectedly exists before apply: %s %s", plan.Family, plan.Table)
	}
	if err := executor.Apply(ctx, plan); err != nil {
		t.Fatalf("apply real Privacy Envelope: %v", err)
	}
	if err := executor.Verify(ctx, plan); err != nil {
		t.Fatalf("verify real Privacy Envelope from nft JSON: %v", err)
	}

	replacement := plan
	replacement.Rules = append([]planner.TunFirewallRulePlan(nil), plan.Rules...)
	replacement.Rules[2].Expr = "ip daddr 198.51.100.20"
	if err := executor.Replace(ctx, plan, replacement); err != nil {
		t.Fatalf("generation-guarded replacement of real Privacy Envelope: %v", err)
	}
	if err := executor.Verify(ctx, replacement); err != nil {
		t.Fatalf("verify replaced real Privacy Envelope from nft JSON: %v", err)
	}

	if err := executor.Remove(ctx, replacement); err != nil {
		t.Fatalf("generation-guarded removal of real Privacy Envelope: %v", err)
	}
	present, err = executor.Exists(ctx, replacement)
	if err != nil {
		t.Fatalf("observe real Privacy Envelope after removal: %v", err)
	}
	if present {
		t.Fatalf("real Privacy Envelope still exists after production removal: %s %s", replacement.Family, replacement.Table)
	}

	foreignAfter := realNFTTableJSON(t, foreignTable)
	if foreignAfter != foreignBefore {
		t.Fatalf("unrelated nftables state changed during Privacy Envelope lifecycle\nbefore: %s\nafter:  %s", foreignBefore, foreignAfter)
	}
}

func createRealNFTForeignSentinel(t *testing.T, table string) {
	t.Helper()
	for _, args := range [][]string{
		{"create", "table", "inet", table},
		{"add", "chain", "inet", table, "sentinel"},
		{"add", "rule", "inet", table, "sentinel", "counter", "comment", "foreign-sentinel"},
	} {
		if output, err := exec.Command("nft", args...).CombinedOutput(); err != nil {
			t.Fatalf("create foreign nftables sentinel with nft %v: %v: %s", args, err, output)
		}
	}
}

func realNFTTableJSON(t *testing.T, table string) string {
	t.Helper()
	output, err := exec.Command("nft", "-j", "list", "table", "inet", table).Output()
	if err != nil {
		t.Fatalf("list real nftables table %s: %v", table, err)
	}
	return string(output)
}

func realNFTPrivacyEnvelopeTable(t *testing.T) string {
	t.Helper()
	return "podlaz_pe_" + realNFTUniqueSuffix(t)
}

func realNFTUniqueSuffix(t *testing.T) string {
	t.Helper()
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generate real nftables test identity: %v", err)
	}
	return hex.EncodeToString(suffix[:])
}
