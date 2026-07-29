package executor

import (
	"context"
	"testing"
)

func TestResolvedDNSExecutorRepairsMissingDeviceAndAcceptsInactiveScope(t *testing.T) {
	runner := &recordingRunner{
		results: []CommandResult{
			{ExitCode: 1, Stderr: `Failed to resolve interface "podlaz0": No such device` + "\n"},
			{},
			{},
			{},
			{Stdout: `Link 7 (podlaz0)
    Current Scopes: none
         Protocols: +DefaultRoute +LLMNR -mDNS -DNSOverTLS DNSSEC=no/unsupported
Current DNS Server: 1.1.1.1
        DNS Servers: 1.1.1.1
         DNS Domain: ~.`},
		},
		errs: []error{executorTestExitError{code: 1}},
	}
	executor := ResolvedDNSExecutor{Runner: runner, VerifyAttempts: 1}
	plan := dnsPlanForTest()

	if _, err := executor.Apply(context.Background(), plan); err != nil {
		t.Fatalf("apply DNS after stale missing-device record: %v", err)
	}
	if err := executor.Verify(context.Background(), plan); err != nil {
		t.Fatalf("verify configured DNS with inactive Current Scopes: %v", err)
	}
}
