package executor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
)

func TestRollbackResourceScopedDoesNotTreatOperationalNoSuchFileAsMissingLink(t *testing.T) {
	recorder := &callRecorder{}
	exec := DNSAwareTunExecutor{
		Base: TunExecutor{
			TunDevice:   fakeTun{rec: recorder},
			TunAddress:  IPTunAddressExecutor{Runner: operationalNoSuchFileRunner{}},
			Routes:      fakeRoutes{rec: recorder},
			PolicyRules: fakeRules{rec: recorder},
		},
	}
	plan := planner.TunPlan{
		TunDevice:  planner.TunDevicePlan{Name: managedTunInterfaceName, Action: "verify"},
		TunAddress: rollbackIdentityAddressPlanForTest(),
	}

	err := exec.RollbackResourceScoped(context.Background(), plan)
	if err == nil {
		t.Fatal("expected rollback identity inspection failure")
	}
	if IsTunRollbackLinkAbsent(err) {
		t.Fatalf("operational no such file error was misclassified as proven missing link: %v", err)
	}
	if !strings.Contains(err.Error(), "no such file or directory") {
		t.Fatalf("expected original operational error to be preserved, got %v", err)
	}
}

type operationalNoSuchFileRunner struct{}

func (operationalNoSuchFileRunner) Run(context.Context, string, ...string) (CommandResult, error) {
	msg := "fork/exec ip: no such file or directory"
	return CommandResult{ExitCode: -1, Stderr: msg, RawStderr: msg}, errors.New(msg)
}
