package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestRunAndPersistTunDiagnosticsDetectsMissingTunThroughProductionPath(t *testing.T) {
	input, network := productionTunDiagnosticInput()
	input.snapshot.TunDevices = nil
	installProductionTunDiagnosticCommandRunner(t, nil)

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	assertProbeClassification(t, report, "session", tundiag.ClassOwnershipMismatch)
	if len(network.tcpCalls) != 0 {
		t.Fatalf("network probes must be skipped after session ownership failure, got TCP calls %v", network.tcpCalls)
	}
}

func TestRunAndPersistTunDiagnosticsDetectsDuplicateResolvedRecordsThroughProductionPath(t *testing.T) {
	input, _ := productionTunDiagnosticInput()
	input.snapshot.DNS.ResolvedLinks = append(input.snapshot.DNS.ResolvedLinks, input.snapshot.DNS.ResolvedLinks[0])
	installProductionTunDiagnosticCommandRunner(t, nil)

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	probe := assertProbeClassification(t, report, "dns-state", tundiag.ClassDNSApplyFailure)
	if !strings.Contains(probe.Error, "duplicate") {
		t.Fatalf("expected duplicate resolved-record evidence, got %#v", probe)
	}
}

func TestRunAndPersistTunDiagnosticsChecksEveryDNSRouteThroughProductionPath(t *testing.T) {
	input, _ := productionTunDiagnosticInput()
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"1.1.1.1": "eth0"})

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	probe := assertProbeClassification(t, report, "dns-state", tundiag.ClassRouteFailure)
	if probe.Evidence.Route == nil || probe.Evidence.Route.Interface != "eth0" {
		t.Fatalf("expected concrete DNS route evidence, got %#v", probe.Evidence)
	}
}

func TestRunAndPersistTunDiagnosticsChecksSystemResolverRouteThroughProductionPath(t *testing.T) {
	input, _ := productionTunDiagnosticInput()
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"198.51.100.10": "eth0"})

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	probe := assertProbeClassification(t, report, "dns-system-resolution", tundiag.ClassRouteFailure)
	if probe.Evidence.Route == nil || probe.Evidence.Route.Interface != "eth0" {
		t.Fatalf("expected system-resolver route evidence, got %#v", probe.Evidence)
	}
}

func TestRunAndPersistTunDiagnosticsChecksRouteBeforeTCP443ThroughProductionPath(t *testing.T) {
	input, network := productionTunDiagnosticInput()
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"198.51.100.20": "eth0"})

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	probe := assertProbeClassification(t, report, "tcp-443", tundiag.ClassRouteFailure)
	if probe.Evidence.Route == nil || probe.Evidence.Route.Interface != "eth0" {
		t.Fatalf("expected TCP target route evidence, got %#v", probe.Evidence)
	}
	for _, call := range network.tcpCalls {
		if strings.HasPrefix(call, "198.51.100.20:") {
			t.Fatalf("TCP connect ran before rejecting the wrong route: %v", network.tcpCalls)
		}
	}
}

func TestRunAndPersistTunDiagnosticsDetectsIPv6UplinkLeakThroughProductionPath(t *testing.T) {
	input, _ := productionTunDiagnosticInput()
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"2001:db8::20": "eth0"})

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	probe := assertProbeClassification(t, report, "ipv6", tundiag.ClassIPv6Leak)
	if probe.Evidence.Route == nil || probe.Evidence.Route.Interface != "eth0" {
		t.Fatalf("expected IPv6 route evidence, got %#v", probe.Evidence)
	}
}

func productionTunDiagnosticInput() (tunDiagnosticInput, *fakeTunDiagnosticNetwork) {
	network := &fakeTunDiagnosticNetwork{}
	return tunDiagnosticInput{
		state: xrayState{
			Connection:    "active",
			Mode:          planner.ModeTun,
			ProfileName:   "Test profile",
			TransactionID: "tx-test",
		},
		coreRunning: true,
		plan: planner.TunPlan{
			TunDevice: planner.TunDevicePlan{Name: netsnapshot.DefaultTunName, MTU: 1500},
			ServerBypass: planner.TunRoutePlan{
				Destination: "203.0.113.10/32",
				Interface:   "eth0",
				Table:       planner.MainRoutingTable,
			},
			DNS: planner.TunDNSPlan{
				TargetLink: netsnapshot.DefaultTunName,
				Servers:    []string{"1.1.1.1"},
			},
		},
		snapshot: netsnapshot.Snapshot{
			DefaultIPv4: netsnapshot.Route{Status: netsnapshot.StatusDetected, Interface: "eth0", Gateway: "192.0.2.1"},
			DefaultIPv6: netsnapshot.Route{Status: netsnapshot.StatusDetected, Interface: netsnapshot.DefaultTunName, Raw: "default dev podlaz0 table 51820"},
			IPv4:        netsnapshot.Finding{Status: netsnapshot.StatusDetected},
			IPv6:        netsnapshot.Finding{Status: netsnapshot.StatusDetected},
			TunDevices:  []netsnapshot.TunDevice{{Name: netsnapshot.DefaultTunName, Status: netsnapshot.StatusDetected}},
			DNS: netsnapshot.DNS{ResolvedLinks: []netsnapshot.ResolvedLink{{
				Name:       netsnapshot.DefaultTunName,
				DNSServers: []string{"1.1.1.1"},
				DNSDomains: []string{"~."},
				Protocols:  []string{"+DefaultRoute"},
			}}},
		},
		network: network,
	}, network
}

func installProductionTunDiagnosticCommandRunner(t *testing.T, routeInterfaces map[string]string) {
	t.Helper()
	original := tunDiagnosticCommandRunner
	tunDiagnosticCommandRunner = func(_ context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		command := strings.TrimSpace(name + " " + strings.Join(args, " "))
		result := tunDiagnosticCommandResult{command: command, exitCode: 0}
		if name != "ip" {
			result.stdout = "table inet podlaz\n"
			return result, nil
		}
		if strings.Join(args, " ") == "-4 rule show" {
			result.stdout = "9999: to 203.0.113.10 lookup main\n10000: from all lookup podlaz\n"
			return result, nil
		}
		if strings.Join(args, " ") == "-6 -brief address show" {
			result.stdout = "eth0 UP 2001:db8:1::2/64\npodlaz0 UNKNOWN 2001:db8:2::2/64\n"
			return result, nil
		}
		if len(args) >= 4 && args[1] == "route" && args[2] == "get" {
			target := args[3]
			iface := routeInterfaces[target]
			if iface == "" {
				if target == "203.0.113.10" {
					iface = "eth0"
				} else {
					iface = netsnapshot.DefaultTunName
				}
			}
			family := "ipv4"
			if strings.Contains(target, ":") {
				family = "ipv6"
			}
			result.stdout = fmt.Sprintf("%s dev %s table 51820 src %s\n", target, iface, diagnosticSourceAddress(family))
			return result, nil
		}
		return tunDiagnosticCommandResult{command: command, exitCode: 1, stderr: "unexpected command"}, errors.New("unexpected command: " + command)
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })
}

func diagnosticSourceAddress(family string) string {
	if family == "ipv6" {
		return "2001:db8:2::2"
	}
	return "198.51.100.2"
}

func assertProbeClassification(t *testing.T, report tundiag.Report, id string, classification tundiag.Classification) tundiag.ProbeResult {
	t.Helper()
	probe, ok := report.Probe(id)
	if !ok {
		t.Fatalf("probe %q not found in report: %#v", id, report.Probes)
	}
	if probe.Status != tundiag.ProbeFail || probe.Classification != classification {
		t.Fatalf("unexpected %s probe: %#v", id, probe)
	}
	return probe
}

type fakeTunDiagnosticNetwork struct {
	tcpCalls []string
}

func (f *fakeTunDiagnosticNetwork) DNSUDP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error) {
	return successfulDiagnosticDNS(), nil
}

func (f *fakeTunDiagnosticNetwork) DNSTCP(context.Context, string, string, uint16) (tundiag.DNSEvidence, error) {
	return successfulDiagnosticDNS(), nil
}

func (f *fakeTunDiagnosticNetwork) Resolve(_ context.Context, name string) ([]string, error) {
	switch name {
	case "podlaz-diagnostic.invalid":
		return nil, &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
	case "example.com":
		return []string{"198.51.100.10"}, nil
	case "www.cloudflare.com":
		return []string{"198.51.100.20", "2001:db8::20"}, nil
	default:
		return nil, fmt.Errorf("unexpected resolve target %q", name)
	}
}

func (f *fakeTunDiagnosticNetwork) TCP(_ context.Context, host string, port uint16) (time.Duration, error) {
	f.tcpCalls = append(f.tcpCalls, net.JoinHostPort(host, strconv.Itoa(int(port))))
	return time.Millisecond, nil
}

func (f *fakeTunDiagnosticNetwork) TLS(context.Context, string, uint16) (tundiag.TLSEvidence, error) {
	return tundiag.TLSEvidence{Version: "TLS 1.3", Cipher: "TLS_AES_128_GCM_SHA256", PeerSubject: "example.test", PeerIssuer: "Example Test CA", HandshakeMS: 1}, nil
}

func (f *fakeTunDiagnosticNetwork) HTTPS(_ context.Context, target tundiag.Target) (tundiag.HTTPEvidence, error) {
	bytesRead := target.MaxResponseBytes
	if bytesRead <= 0 {
		bytesRead = 1
	}
	return tundiag.HTTPEvidence{StatusCode: 204, BytesRead: bytesRead, HeaderMS: 1, BodyMS: 1}, nil
}

func (f *fakeTunDiagnosticNetwork) DoH(_ context.Context, target tundiag.Target, name string, recordType uint16) (tundiag.DNSEvidence, tundiag.HTTPEvidence, error) {
	dns := successfulDiagnosticDNS()
	dns.Server = target.URL
	dns.Name = name
	dns.Type = recordType
	return dns, tundiag.HTTPEvidence{StatusCode: 200, BytesRead: 32}, nil
}

func (f *fakeTunDiagnosticNetwork) IsNXDomain(err error) bool {
	var dnsError *net.DNSError
	return errors.As(err, &dnsError) && dnsError.IsNotFound
}

func successfulDiagnosticDNS() tundiag.DNSEvidence {
	return tundiag.DNSEvidence{
		Server:       "1.1.1.1:53",
		Name:         "example.com",
		Type:         tundiag.DNSRecordTypeA,
		ResponseCode: tundiag.DNSRCodeSuccess,
		Addresses:    []string{"198.51.100.10"},
		MessageID:    1,
	}
}
