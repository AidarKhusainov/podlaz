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
	runner := exactNFTLifecycleRunner{nftOutput: `{"nftables":[
{"metainfo":{"version":"1.0.9","release_name":"Old Doc Yak","json_schema_version":1}},
{"table":{"family":"inet","name":"podlaz","handle":10}},
{"chain":{"family":"inet","table":"podlaz","name":"output","handle":1,"type":"filter","hook":"output","prio":0,"policy":"accept"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":1,"expr":[{"match":{"op":"==","left":{"meta":{"key":"oifname"}},"right":"podlaz0"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"podlaz:firewall:tun-egress"}},
{"rule":{"family":"inet","table":"podlaz","chain":"output","handle":2,"expr":[{"match":{"op":"==","left":{"meta":{"key":"l4proto"}},"right":"tcp"}},{"counter":{"packets":0,"bytes":0}},{"accept":null}],"comment":"foreign-extra"}}
]}`}

	check := staleResources(context.Background(), runner, staleResourceOptions{
		ipPath: "/usr/bin/ip", ipOK: true,
		nftPath: "/usr/sbin/nft", nftOK: true,
		runtimeDir: t.TempDir(), runtimeDirOwnedByDaemon: true,
		lifecycle: LifecycleDiagnosticContext{
			State:              LifecycleActiveTUN,
			TransactionID:      "tx-active",
			TransactionState:   txstate.TransactionCommitted,
			Interface:          ManagedResourceExpectedOwned,
			InterfaceLinkIndex: 7,
			InterfaceLinkKind:  "tun",
			NFTTable:           ManagedResourceExpectedOwned,
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
	case "nft -j list table inet podlaz":
		return CommandResult{Stdout: r.nftOutput}, nil
	default:
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + command)
	}
}
