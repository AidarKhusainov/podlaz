package doctor

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestStaleResourcesRejectsChangedActiveNFTTableWithSameFamilyAndName(t *testing.T) {
	plan := lifecycleNFTPlanForTest()
	runner := exactNFTLifecycleRunner{nftOutput: `table inet podlaz {
	chain output {
		type filter hook output priority 0; policy accept;
		oifname "podlaz0" counter packets 0 bytes 0 accept comment "podlaz:firewall:tun-egress"
		meta l4proto tcp counter packets 0 bytes 0 accept comment "foreign-extra"
	}
}`}

	check := staleResources(context.Background(), runner, staleResourceOptions{
		ipPath: "/usr/bin/ip", ipOK: true,
		nftPath: "/usr/sbin/nft", nftOK: true,
		runtimeDir: t.TempDir(), runtimeDirOwnedByDaemon: true,
		lifecycle: LifecycleDiagnosticContext{
			State:              LifecycleActiveTUN,
			TransactionID:      "tx-active",
			TransactionState:   txstate.TransactionCommitted,
			Interface:          ManagedResourceExactOwned,
			InterfaceLinkIndex: 7,
			InterfaceLinkKind:  "tun",
			NFTTable:           ManagedResourceExactOwned,
			NFTPlan:            &plan,
		},
	})

	if check.Severity != SeverityWarning {
		t.Fatalf("changed active nftables composition must fail closed, got %#v", check)
	}
	if !strings.Contains(check.Message, "nft table inet podlaz does not match active transaction") {
		t.Fatalf("changed active nftables composition was not actionable: %#v", check)
	}
}

func lifecycleNFTPlanForTest() planner.TunFirewallPlan {
	return planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      "inet",
		Table:       "podlaz",
		TableAction: planner.FirewallTableAction,
		Chains: []planner.TunFirewallChainPlan{{
			Name:     planner.FirewallOutputChain,
			Type:     planner.FirewallChainTypeFilter,
			Hook:     planner.FirewallOutputHook,
			Priority: planner.FirewallOutputPriority,
			Policy:   planner.FirewallDefaultChainPolicy,
			Action:   planner.FirewallTableAction,
		}},
		Rules: []planner.TunFirewallRulePlan{{
			Chain:       planner.FirewallOutputChain,
			Expr:        `oifname "podlaz0"`,
			Verdict:     planner.FirewallVerdictAccept,
			Action:      planner.FirewallActionAdd,
			Ownership:   planner.FirewallTunEgressOwner,
			RollbackKey: planner.FirewallTunEgressKey,
		}},
	}
}

type exactNFTLifecycleRunner struct {
	nftOutput string
}

func (r exactNFTLifecycleRunner) LookPath(file string) (string, error) {
	return filepath.Join("/usr/bin", file), nil
}

func (r exactNFTLifecycleRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := filepath.Base(name) + " " + strings.Join(args, " ")
	switch command {
	case "ip -details -o link show dev podlaz0":
		return CommandResult{Stdout: "7: podlaz0: <POINTOPOINT,UP> mtu 1500 tun type tun"}, nil
	case "nft -y list table inet podlaz":
		return CommandResult{Stdout: r.nftOutput}, nil
	default:
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + command)
	}
}
