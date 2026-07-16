package daemon

import (
	"bytes"
	"context"
	"errors"
	"log"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	txstate "github.com/AidarKhusainov/podlaz/internal/state"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestBeginTunTransactionPersistsDiagnosticServerMetadata(t *testing.T) {
	runtimeDir := t.TempDir()
	result, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{
		ID: "profile-test", Server: "vpn.example.test", Port: 443, ServerName: "edge.example.test",
	}, planner.TunPlan{Mode: planner.ModeTun, ProfileID: "profile-test"}, fixedClock())
	if err != nil {
		t.Fatal(err)
	}
	tx, _, err := (txstate.TransactionStore{RuntimeDir: runtimeDir}).Load(result.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, serverName := tunDiagnosticServerMetadata(tx)
	if endpoint != "vpn.example.test:443" || serverName != "edge.example.test" {
		t.Fatalf("unexpected transaction diagnostic metadata endpoint=%q server_name=%q labels=%#v", endpoint, serverName, tx.Labels)
	}
}

func TestLogConnectFailureIncludesStructuredTunDiagnosticFields(t *testing.T) {
	var output bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&output)
	log.SetFlags(0)
	log.SetPrefix("")
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})

	err := withTunFailureDiagnosticSummary(errors.New("verification failed"), tunFailureDiagnosticSummary{
		PrimaryClassification: tundiag.ClassDNSUDPFailure,
		ReportPath:            filepath.Join("run", "podlaz", "diagnostics", tundiag.LastReportFileName),
		Persisted:             true,
	})
	logConnectFailure(api.ConnectRequest{Mode: planner.ModeTun}, err)
	line := output.String()
	if !strings.Contains(line, "tun_primary_classification=dns_udp_failure") {
		t.Fatalf("missing primary TUN classification field: %s", line)
	}
	if !strings.Contains(line, "tun_report_location=run_podlaz_diagnostics_tun-last.json") {
		t.Fatalf("missing report location field: %s", line)
	}
}
