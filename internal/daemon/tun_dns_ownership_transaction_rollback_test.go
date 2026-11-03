package daemon

import (
	"context"
	"errors"
	"strings"
	"testing"

	netexecutor "github.com/AidarKhusainov/podlaz/internal/network/executor"
	"github.com/AidarKhusainov/podlaz/internal/profile"
)

const foreignDefaultDNSForTransactionTest = productionResolvedInactiveScopeForTest + `

Link 9 (wg0)
    Current Scopes: DNS
         Protocols: +DefaultRoute +LLMNR
Current DNS Server: 198.51.100.53
       DNS Servers: 198.51.100.53
        DNS Domain: ~.`

// resolvedOwnershipChangeRunner models a separate network service changing
// its own resolved link between Podlaz's read-only observations. Delegating
// every host mutation to the existing production-composition fake also checks
// that none of Podlaz's rollback commands target the foreign link.
type resolvedOwnershipChangeRunner struct {
	*productionTunCommandRunner
	observations []string
}

func (r *resolvedOwnershipChangeRunner) Run(ctx context.Context, name string, args ...string) (netexecutor.CommandResult, error) {
	if name == "resolvectl" && len(args) == 2 && args[0] == "status" && args[1] == "--no-pager" && len(r.observations) > 0 {
		r.productionTunCommandRunner.resolvedStatus = r.observations[0]
		r.observations = r.observations[1:]
	}
	return r.productionTunCommandRunner.Run(ctx, name, args...)
}

func TestProductionDNSOwnershipChangePreservesForeignStateAfterExactRollback(t *testing.T) {
	t.Setenv(e2eTunHookGateEnv, "")
	t.Setenv(e2eTunHookPhaseEnv, "")

	tests := []struct {
		name                string
		observations        []string
		wantUnknown         bool
		wantDNSRevert       int
		wantStatusReads     int
	}{
		{
			name:            "foreign default owner appears during route apply",
			observations:    []string{productionResolvedInactiveScopeForTest, foreignDefaultDNSForTransactionTest},
			wantStatusReads: 2,
		},
		{
			name: "foreign default owner appears during DNS verification",
			observations: []string{
				productionResolvedInactiveScopeForTest,
				productionResolvedInactiveScopeForTest,
				foreignDefaultDNSForTransactionTest,
			},
			wantDNSRevert:   2, // own DNS apply and exact rollback, never wg0
			wantStatusReads: 3,
		},
		{
			name:            "resolver inspection becomes inconclusive after base apply",
			observations:    []string{productionResolvedInactiveScopeForTest, ""},
			wantUnknown:     true,
			wantStatusReads: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runtimeDir := t.TempDir()
			runner := &resolvedOwnershipChangeRunner{
				productionTunCommandRunner: newProductionTunCommandRunner(productionResolvedInactiveScopeForTest),
				observations:             tt.observations,
			}
			executor := newProductionTunPlanExecutor(runner)
			plan := productionDNSPlanForTest()
			profile := profile.Profile{ID: "example-profile", Name: "Example Profile"}
			result, err := beginTunTransaction(context.Background(), runtimeDir, profile, plan, fixedClock())
			if err != nil {
				t.Fatalf("begin production transaction: %v", err)
			}
			err = applyVerifyTunTransaction(context.Background(), result, executor)
			var ownership *netexecutor.DefaultDNSOwnershipError
			if !errors.As(err, &ownership) || ownership.Unknown != tt.wantUnknown {
				t.Fatalf("expected typed DNS ownership failure with unknown=%t, got %T: %v", tt.wantUnknown, err, err)
			}
			if !tt.wantUnknown && ownership.Link != "wg0" {
				t.Fatalf("observed foreign owner = %q, want wg0", ownership.Link)
			}
			if err == nil || !strings.Contains(err.Error(), "rolled back applied podlaz-owned") {
				t.Fatalf("exact Podlaz rollback must be confirmed after late DNS conflict: %v", err)
			}
			if runner.tunAddress || runner.nftTable || len(runner.routes) != 0 || len(runner.rules) != 0 {
				t.Fatalf("Podlaz rollback left network state: address=%t nft=%t routes=%#v rules=%#v",
					runner.tunAddress, runner.nftTable, runner.routes, runner.rules)
			}
			if got := runner.count("resolvectl revert podlaz0"); got != tt.wantDNSRevert {
				t.Fatalf("owned DNS reverts = %d, want %d; commands=%#v", got, tt.wantDNSRevert, runner.commands)
			}
			if got := runner.count("resolvectl status --no-pager"); got != tt.wantStatusReads {
				t.Fatalf("ownership observations = %d, want %d", got, tt.wantStatusReads)
			}
			for _, command := range runner.commands {
				if strings.HasPrefix(command, "resolvectl ") && strings.Contains(command, " wg0") {
					t.Fatalf("Podlaz command mutated a foreign DNS link: %q", command)
				}
			}
			if !tt.wantUnknown && runner.resolvedStatus != foreignDefaultDNSForTransactionTest {
				t.Fatal("the foreign default DNS owner disappeared or was modified by Podlaz rollback")
			}
			assertNoProductionTunTransactionBlocker(t, runtimeDir)
		})
	}
}
