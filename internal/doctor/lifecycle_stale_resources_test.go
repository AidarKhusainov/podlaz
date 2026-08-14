package doctor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	txstate "github.com/AidarKhusainov/podlaz/internal/state"
)

func TestStaleResourcesTreatsExactActiveOwnedResourcesAsExpected(t *testing.T) {
	check := staleResources(context.Background(), lifecycleResourceRunner{interfacePresent: true, nftPresent: true}, staleResourceOptions{
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
		},
	})
	if check.Severity != SeverityOK {
		t.Fatalf("expected exact active owned resources to be healthy, got %#v", check)
	}
	if strings.Contains(check.Message, "stale") || strings.Contains(check.Message, " exists") {
		t.Fatalf("active owned resources were described as stale: %q", check.Message)
	}
}

func TestStaleResourcesWarnsWhenActiveOwnedResourceIsMissing(t *testing.T) {
	check := staleResources(context.Background(), lifecycleResourceRunner{interfacePresent: false, nftPresent: true}, staleResourceOptions{
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
		},
	})
	if check.Severity != SeverityWarning || !strings.Contains(check.Message, "expected interface podlaz0 is missing") {
		t.Fatalf("expected missing active interface warning, got %#v", check)
	}
}

func TestStaleResourcesRemainsConservativeWithoutLifecycleAuthority(t *testing.T) {
	check := staleResources(context.Background(), lifecycleResourceRunner{interfacePresent: true, nftPresent: true}, staleResourceOptions{
		ipPath: "/usr/bin/ip", ipOK: true,
		nftPath: "/usr/sbin/nft", nftOK: true,
		runtimeDir: t.TempDir(),
	})
	if check.Severity != SeverityWarning || !strings.Contains(check.Message, "interface podlaz0 exists") || !strings.Contains(check.Message, "nft table inet podlaz exists") {
		t.Fatalf("local fallback stopped being fail-closed: %#v", check)
	}
}

func TestStaleResourcesDoesNotTrustUnprovenActiveOwnership(t *testing.T) {
	check := staleResources(context.Background(), lifecycleResourceRunner{interfacePresent: true, nftPresent: true}, staleResourceOptions{
		ipPath: "/usr/bin/ip", ipOK: true,
		nftPath: "/usr/sbin/nft", nftOK: true,
		runtimeDir: t.TempDir(), runtimeDirOwnedByDaemon: true,
		lifecycle: LifecycleDiagnosticContext{
			State:            LifecycleActiveTUN,
			TransactionID:    "tx-active",
			TransactionState: txstate.TransactionCommitted,
			Interface:        ManagedResourceUnproven,
			NFTTable:         ManagedResourceExactOwned,
		},
	})
	if check.Severity != SeverityWarning || !strings.Contains(check.Message, "cannot prove interface podlaz0 belongs to the active transaction") {
		t.Fatalf("unproven ownership was treated as healthy: %#v", check)
	}
}

func TestStaleResourcesRejectsMismatchedActiveLinkIdentity(t *testing.T) {
	check := staleResources(context.Background(), lifecycleResourceRunner{interfacePresent: true, interfaceIndex: 8, nftPresent: true}, staleResourceOptions{
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
		},
	})
	if check.Severity != SeverityWarning || !strings.Contains(check.Message, "cannot prove interface podlaz0 belongs to the active transaction") {
		t.Fatalf("foreign-looking link identity was trusted: %#v", check)
	}
}

func TestStaleResourcesKeepsCleanupRequiredTransactionUnhealthy(t *testing.T) {
	check := staleResources(context.Background(), lifecycleResourceRunner{}, staleResourceOptions{
		ipPath: "/usr/bin/ip", ipOK: true,
		nftPath: "/usr/sbin/nft", nftOK: true,
		runtimeDir: t.TempDir(), runtimeDirOwnedByDaemon: true,
		lifecycle: LifecycleDiagnosticContext{
			State:                      LifecycleActiveTUN,
			TransactionID:              "tx-cleanup",
			TransactionState:           txstate.TransactionFailed,
			TransactionRequiresCleanup: true,
			Interface:                  ManagedResourceUnproven,
			NFTTable:                   ManagedResourceUnproven,
		},
	})
	if check.Severity != SeverityWarning || !strings.Contains(check.Message, "active transaction tx-cleanup requires cleanup") {
		t.Fatalf("cleanup-required active transaction was treated as healthy: %#v", check)
	}
}

type lifecycleResourceRunner struct {
	interfacePresent bool
	interfaceIndex   int
	nftPresent       bool
}

func (r lifecycleResourceRunner) LookPath(file string) (string, error) {
	return filepath.Join("/usr/bin", file), nil
}

func (r lifecycleResourceRunner) Run(_ context.Context, name string, args ...string) (CommandResult, error) {
	command := filepath.Base(name) + " " + strings.Join(args, " ")
	switch command {
	case "ip link show dev podlaz0", "ip -details -o link show dev podlaz0":
		if r.interfacePresent {
			index := r.interfaceIndex
			if index == 0 {
				index = 7
			}
			return CommandResult{Stdout: fmt.Sprintf("%d: podlaz0: <POINTOPOINT,UP> mtu 1500 tun type tun", index)}, nil
		}
		return CommandResult{Stderr: "Device podlaz0 does not exist", ExitCode: 1}, errors.New("exit status 1")
	case "nft list table inet podlaz":
		if r.nftPresent {
			return CommandResult{Stdout: "table inet podlaz {}"}, nil
		}
		return CommandResult{Stderr: "No such table", ExitCode: 1}, errors.New("exit status 1")
	default:
		return CommandResult{ExitCode: -1}, errors.New("unexpected command: " + command)
	}
}
