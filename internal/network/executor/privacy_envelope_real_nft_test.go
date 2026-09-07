package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPrivacyEnvelopeRealNftRoundTrip(t *testing.T) {
	if os.Getenv("PODLAZ_TEST_REAL_NFT") != "1" {
		t.Skip("set PODLAZ_TEST_REAL_NFT=1 to run the privileged nftables round trip")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		t.Fatalf("real nft test requires sudo: %v", err)
	}
	if _, err := exec.LookPath("nft"); err != nil {
		t.Fatalf("real nft test requires nft: %v", err)
	}

	plan := productionShapedPrivacyEnvelopePlanForTest()
	plan.Table = fmt.Sprintf("podlaz_pe_%012x", uint64(os.Getpid()))
	runner := sudoCommandRunner{}
	executor := PrivacyEnvelopeExecutor{Runner: runner, ScriptDir: t.TempDir()}
	ctx := context.Background()

	// The table name is unique to this process, but cleanup first keeps the test
	// idempotent after a locally interrupted run.
	_, _ = runner.Run(ctx, "nft", "delete", "table", plan.Family, plan.Table)
	t.Cleanup(func() {
		_, _ = runner.Run(context.Background(), "nft", "delete", "table", plan.Family, plan.Table)
	})

	if err := executor.Apply(ctx, plan); err != nil {
		t.Fatalf("apply real Privacy Envelope: %v", err)
	}
	if err := executor.Verify(ctx, plan); err != nil {
		t.Fatalf("verify real Privacy Envelope after nft canonicalization: %v", err)
	}
	if err := executor.Remove(ctx, plan); err != nil {
		t.Fatalf("remove real Privacy Envelope: %v", err)
	}
	exists, err := executor.Exists(ctx, plan)
	if err != nil {
		t.Fatalf("observe removed Privacy Envelope: %v", err)
	}
	if exists {
		t.Fatal("Privacy Envelope remained after exact removal")
	}
}

type sudoCommandRunner struct{}

func (sudoCommandRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	sudoArgs := append([]string{"-n", name}, args...)
	cmd := exec.CommandContext(ctx, "sudo", sudoArgs...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	rawStdout := stdout.String()
	rawStderr := stderr.String()
	result := CommandResult{
		Stdout:    strings.TrimSpace(rawStdout),
		Stderr:    strings.TrimSpace(rawStderr),
		RawStdout: rawStdout,
		RawStderr: rawStderr,
	}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}
