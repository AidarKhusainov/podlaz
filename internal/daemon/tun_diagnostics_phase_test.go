package daemon

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestPhaseAwareProductionTCPAdapterMarksRouteLookupTimeout(t *testing.T) {
	installDiagnosticResolver(t)
	original := tunDiagnosticCommandRunner
	tunDiagnosticCommandRunner = func(ctx context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		command := strings.TrimSpace(name + " " + strings.Join(args, " "))
		if name == "ip" && len(args) >= 4 && args[1] == "route" && args[2] == "get" {
			<-ctx.Done()
			return tunDiagnosticCommandResult{command: command, exitCode: -1}, ctx.Err()
		}
		return original(ctx, name, args...)
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result := buildPhaseAwareTunDiagnosticAdapters(productionTunDiagnosticInput()).TCP443(ctx)
	if result.Status != tundiag.ProbeFail || result.FailurePhase != tundiag.FailurePhaseRouteLookup {
		t.Fatalf("expected route lookup phase, got %#v", result)
	}
}

func TestPhaseAwareTLSProbeMarksTCPConnectTimeout(t *testing.T) {
	client := tundiag.NetworkClient{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, endpointTimeoutError{}
	}}
	result := probeTunTLSWithPhase(context.Background(), client, "www.example.test", 443)
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassTLSFailure || result.FailurePhase != tundiag.FailurePhaseTCPConnect {
		t.Fatalf("expected TCP connect phase, got %#v", result)
	}
}

func TestPhaseAwareHTTPSProbeMarksTCPConnectTimeout(t *testing.T) {
	client := tundiag.NetworkClient{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, endpointTimeoutError{}
	}}
	result := probeTunHTTPSWithPhase(context.Background(), client, "https-google-small", tundiag.ClassHTTPSFailure)
	if result.Status != tundiag.ProbeFail || result.Classification != tundiag.ClassHTTPSFailure || result.FailurePhase != tundiag.FailurePhaseTCPConnect {
		t.Fatalf("expected TCP connect phase, got %#v", result)
	}
}

type endpointTimeoutError struct{}

func (endpointTimeoutError) Error() string   { return "endpoint timeout" }
func (endpointTimeoutError) Timeout() bool   { return true }
func (endpointTimeoutError) Temporary() bool { return true }
