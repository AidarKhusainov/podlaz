package daemon

import (
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/recovery"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestStartupScanDoctorUsesActiveConnectionWordingForCleanCommittedTUN(t *testing.T) {
	status := api.StatusResponse{
		Connection:          "active",
		Mode:                planner.ModeTun,
		ActiveTransactionID: "tx-active",
		Transactions: []api.TransactionStatus{{
			ID:              "tx-active",
			State:           string(txstate.TransactionCommitted),
			RequiresCleanup: false,
		}},
	}
	response := withStartupScanDoctor(api.DoctorResponse{}, recovery.PlanResult{}, status)
	check := response.Checks[len(response.Checks)-1]
	if !strings.Contains(check.Message, "clean for active connection") || strings.Contains(check.Message, "clean inactive state") {
		t.Fatalf("clean active startup scan has misleading wording: %#v", check)
	}
}

func TestStartupScanDoctorKeepsInactiveWordingForCleanInactiveState(t *testing.T) {
	response := withStartupScanDoctor(api.DoctorResponse{}, recovery.PlanResult{}, api.StatusResponse{Connection: "inactive"})
	check := response.Checks[len(response.Checks)-1]
	if !strings.Contains(check.Message, "clean inactive state") {
		t.Fatalf("clean inactive startup scan wording changed unexpectedly: %#v", check)
	}
}
