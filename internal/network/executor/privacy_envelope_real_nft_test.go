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
	if err := executor.Remove(ctx, plan); err != nil {
		t.Fatalf("generation-guarded removal of real Privacy Envelope: %v", err)
	}
	present, err = executor.Exists(ctx, plan)
	if err != nil {
		t.Fatalf("observe real Privacy Envelope after removal: %v", err)
	}
	if present {
		t.Fatalf("real Privacy Envelope still exists after production removal: %s %s", plan.Family, plan.Table)
	}
}

func realNFTPrivacyEnvelopeTable(t *testing.T) string {
	t.Helper()
	var suffix [6]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatalf("generate real nftables test identity: %v", err)
	}
	return "podlaz_pe_" + hex.EncodeToString(suffix[:])
}
