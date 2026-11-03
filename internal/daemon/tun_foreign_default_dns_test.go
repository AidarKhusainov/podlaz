package daemon

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/AidarKhusainov/podlaz/internal/api"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
)

func TestPrepareTunCoexistenceRejectsCompetingDefaultDNSWithoutMutation(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	snapshot := netsnapshot.FakeDesktopWithForeignDefaultDNSOwner()
	before := snapshot

	got, err := manager.prepareTunCoexistence(context.Background(), snapshot, api.HandoffBlock, netsnapshot.Options{})
	if err == nil {
		t.Fatal("an independently owned, active foreign ~. DNS link must block before Xray or networking starts")
	}
	if status := daemonAPIHTTPStatusCode(err); status != http.StatusConflict {
		t.Fatalf("foreign default DNS ownership status = %d, want 409: %v", status, err)
	}
	if !strings.Contains(err.Error(), "default DNS") {
		t.Fatalf("missing user-facing DNS ownership explanation: %v", err)
	}
	if strings.Contains(err.Error(), "198.51.100.53") {
		t.Fatalf("foreign DNS server leaked through default connection error: %v", err)
	}
	if !reflect.DeepEqual(got, before) {
		t.Fatalf("preflight changed foreign state observation: got=%#v want=%#v", got, before)
	}
}

func TestPrepareTunCoexistenceAllowsForeignTunnelWithoutCompetingDefaultDNS(t *testing.T) {
	manager := NewXrayManager(t.TempDir())
	snapshot := netsnapshot.FakeDesktopWithServerRouteViaForeignVPN()

	got, err := manager.prepareTunCoexistence(context.Background(), snapshot, api.HandoffBlock, netsnapshot.Options{})
	if err != nil {
		t.Fatalf("foreign VPN without route-only default DNS ownership must remain baseline: %v", err)
	}
	if !reflect.DeepEqual(got, snapshot) {
		t.Fatal("foreign baseline observation changed")
	}
}
