package daemon

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/profile"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestCollectTunFailureDiagnosticsDoesNotExposeNetworkOrProfileIdentifiers(t *testing.T) {
	const (
		profileID      = "profile-private-id-9q"
		profileName    = "Profile Alpha 9Q"
		serverHost     = "vpn.private.example.test"
		serverName     = "sni.private.example.test"
		serverAddress  = "192.0.2.44"
		dnsServer      = "192.0.2.53"
		physicalDevice = "wlan-private0"
		ssid           = "Corp WiFi 9Q"
		gateway        = "192.0.2.1"
	)

	runtimeDir := t.TempDir()
	manager := NewXrayManager(runtimeDir)
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		return netsnapshot.Snapshot{
			DefaultIPv4: netsnapshot.Route{
				Status:    netsnapshot.StatusDetected,
				Interface: physicalDevice,
				Gateway:   gateway,
				Raw:       "default via " + gateway + " dev " + physicalDevice,
			},
			NetworkManager: netsnapshot.NetworkManager{ActiveConnections: []netsnapshot.NetworkManagerConnection{{
				Name:   ssid,
				Device: physicalDevice,
				State:  "activated",
			}}},
			IPv4: netsnapshot.Finding{Status: netsnapshot.StatusDetected},
			IPv6: netsnapshot.Finding{Status: netsnapshot.StatusMissing},
		}
	}

	plan := transactionPlanForTest()
	plan.ProfileID = profileID
	plan.ProfileName = profileName
	plan.DNS.TargetLink = netsnapshot.DefaultTunName
	plan.DNS.Servers = []string{dnsServer}
	plan.ServerBypass.Destination = serverAddress + "/32"
	plan.ServerBypass.Interface = physicalDevice
	plan.ServerBypass.Gateway = gateway

	transaction, err := beginTunTransaction(context.Background(), runtimeDir, profile.Profile{
		ID:         profileID,
		Name:       profileName,
		Server:     serverHost,
		Port:       443,
		ServerName: serverName,
	}, plan, fixedClock())
	if err != nil {
		t.Fatalf("begin TUN transaction: %v", err)
	}

	cause := &tunNetworkMutationError{
		phase: "network-apply",
		cause: errors.New("dial " + serverHost + " at " + serverAddress + " through " + physicalDevice + " on " + ssid + " for " + profileName),
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	summary := manager.collectTunFailureDiagnostics(ctx, transaction.TransactionID, plan, cause)
	if !summary.Persisted {
		t.Fatalf("failed-connect diagnostics were not persisted: %#v", summary)
	}

	store := tundiag.Store{RuntimeDir: runtimeDir}
	persisted, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatalf("read persisted failed-connect report: %v", err)
	}
	loaded, _, err := store.Load()
	if err != nil {
		t.Fatalf("load persisted failed-connect report: %v", err)
	}
	var jsonOutput bytes.Buffer
	if err := tundiag.WriteJSON(&jsonOutput, loaded); err != nil {
		t.Fatalf("render failed-connect JSON: %v", err)
	}
	outputs := map[string]string{
		"persisted JSON": string(persisted),
		"client JSON":    jsonOutput.String(),
		"human output":   tundiag.RenderHuman(loaded, true),
	}
	sensitive := []string{
		profileID,
		profileName,
		transaction.TransactionID,
		serverHost,
		serverName,
		serverAddress,
		dnsServer,
		physicalDevice,
		ssid,
		gateway,
	}
	for outputName, output := range outputs {
		for _, value := range sensitive {
			if strings.Contains(output, value) {
				t.Fatalf("%s leaked failed-connect identifier %q: %s", outputName, value, output)
			}
		}
	}
}
