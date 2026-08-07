package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/recovery"
)

func TestRecoverJSONIsCleanForExactExitZeroResolvedMissingStatus(t *testing.T) {
	runner := issue243RecoveryRunner{}
	plan := recovery.PlanWithOptions(context.Background(), recovery.Options{
		Runner:     runner,
		RuntimeDir: filepath.Join(t.TempDir(), "podlaz"),
	})
	if len(plan.Candidates) != 0 || len(plan.Warnings) != 0 {
		t.Fatalf("precondition: exit-0 missing status must produce a clean plan, got %#v", plan)
	}

	var out bytes.Buffer
	err := runRecoverCommand(context.Background(), []string{"--json"}, &out, options{
		recover: func(context.Context) recovery.PlanResult { return plan },
	})
	if err != nil {
		t.Fatalf("recover --json returned an error for a clean plan: %v", err)
	}

	var payload struct {
		Status   string   `json:"status"`
		Warnings []string `json:"warnings"`
		Recovery struct {
			Candidates []map[string]any `json:"candidates"`
			Warnings   []map[string]any `json:"warnings"`
		} `json:"recovery"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("decode recover JSON: %v; output=%q", err, out.String())
	}
	if payload.Status != "ok" {
		t.Fatalf("clean recovery JSON must report status ok, got %q", payload.Status)
	}
	if len(payload.Warnings) != 0 || len(payload.Recovery.Candidates) != 0 || len(payload.Recovery.Warnings) != 0 {
		t.Fatalf("clean recovery JSON must not publish warnings or candidates, got %s", out.String())
	}
}

type issue243RecoveryRunner struct{}

func (issue243RecoveryRunner) LookPath(file string) (string, error) {
	switch file {
	case "ip":
		return "/usr/sbin/ip", nil
	case "nft":
		return "/usr/sbin/nft", nil
	case "resolvectl":
		return "/usr/bin/resolvectl", nil
	default:
		return "", errors.New("command not found")
	}
}

func (issue243RecoveryRunner) Run(_ context.Context, name string, args ...string) (recovery.CommandResult, error) {
	key := filepath.Base(name) + " " + strings.Join(args, " ")
	switch key {
	case "ip link show dev podlaz0":
		return recovery.CommandResult{
			Stderr:    `Device "podlaz0" does not exist.`,
			RawStderr: `Device "podlaz0" does not exist.`,
			ExitCode:  1,
		}, errors.New("exit status 1")
	case "resolvectl status podlaz0 --no-pager":
		const stderr = `Failed to resolve interface "podlaz0", ignoring: No such device`
		return recovery.CommandResult{
			Stderr:    stderr,
			RawStderr: stderr + "\n",
			ExitCode:  0,
		}, nil
	case "nft list table inet podlaz":
		return recovery.CommandResult{
			Stderr:    "Error: No such file or directory",
			RawStderr: "Error: No such file or directory",
			ExitCode:  1,
		}, errors.New("exit status 1")
	default:
		return recovery.CommandResult{ExitCode: -1}, errors.New("unexpected command: " + key)
	}
}
