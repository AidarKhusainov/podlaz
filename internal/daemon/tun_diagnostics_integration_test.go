package daemon

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func TestCollectTunFailureDiagnosticsPreservesParentCancellation(t *testing.T) {
	original := tunDiagnosticCommandRunner
	commandCalls := 0
	tunDiagnosticCommandRunner = func(ctx context.Context, name string, args ...string) (tunDiagnosticCommandResult, error) {
		commandCalls++
		return tunDiagnosticCommandResult{command: strings.TrimSpace(name + " " + strings.Join(args, " ")), exitCode: -1}, ctx.Err()
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })

	manager := &XrayManager{RuntimeDir: t.TempDir()}
	manager.snapshotCollector = func(context.Context, netsnapshot.Options) netsnapshot.Snapshot {
		return productionTunDiagnosticInput().snapshot
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()
	_ = manager.collectTunFailureDiagnostics(ctx, "tx-test", productionTunDiagnosticInput().plan, errors.New("verification failed"))
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled diagnostics delayed rollback for %s", elapsed)
	}
	if commandCalls != 0 {
		t.Fatalf("cancelled parent must stop pre-rollback command probes, got %d calls", commandCalls)
	}
}

func TestRunAndPersistTunDiagnosticsDetectsMissingTunThroughProductionPath(t *testing.T) {
	input := productionTunDiagnosticInput()
	input.snapshot.TunDevices = nil
	installProductionTunDiagnosticCommandRunner(t, nil)

	report := (&XrayManager{RuntimeDir: t.TempDir()}).runAndPersistTunDiagnostics(context.Background(), input)
	assertProbeClassification(t, report, "session", tundiag.ClassOwnershipMismatch)
}

func TestProductionAdaptersDetectMissingExpectedNftablesState(t *testing.T) {
	input := productionTunDiagnosticInput()
	input.plan.Firewall = planner.TunFirewallPlan{
		Backend:     planner.FirewallBackendNftables,
		Family:      netsnapshot.DefaultNFTFamily,
		Table:       netsnapshot.DefaultNFTTable,
		TableAction: planner.FirewallTableAction,
	}
	input.snapshot.Nftables.PodlazTable.Status = netsnapshot.StatusMissing

	report := runProductionAdapter(t, input, "session")
	assertProbeClassification(t, report, "session", tundiag.ClassOwnershipMismatch)
}

func TestProductionAdaptersDetectDuplicateResolvedRecords(t *testing.T) {
	input := productionTunDiagnosticInput()
	input.snapshot.DNS.ResolvedLinks = append(input.snapshot.DNS.ResolvedLinks, input.snapshot.DNS.ResolvedLinks[0])

	report := runProductionAdapter(t, input, "dns-state")
	probe := assertProbeClassification(t, report, "dns-state", tundiag.ClassDNSApplyFailure)
	if probe.Error != "[detail omitted by diagnostic privacy policy]" {
		t.Fatalf("expected private duplicate resolved-record evidence, got %#v", probe)
	}
}

func TestProductionAdaptersCheckEveryDNSRoute(t *testing.T) {
	input := productionTunDiagnosticInput()
	input.plan.DNS.Servers = []string{"192.0.2.53"}
	input.snapshot.DNS.ResolvedLinks[0].DNSServers = []string{"192.0.2.53"}
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"192.0.2.53": "eth0"})

	report := runProductionAdapter(t, input, "dns-state")
	probe := assertProbeClassification(t, report, "dns-state", tundiag.ClassRouteFailure)
	assertPrivateRouteEvidence(t, probe.Evidence)
}

func TestProductionAdaptersCheckSystemResolverRoute(t *testing.T) {
	input := productionTunDiagnosticInput()
	installDiagnosticResolver(t)
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"198.51.100.10": "eth0"})

	report := runProductionAdapter(t, input, "dns-system-resolution")
	probe := assertProbeClassification(t, report, "dns-system-resolution", tundiag.ClassRouteFailure)
	assertPrivateRouteEvidence(t, probe.Evidence)
}

func TestProductionAdaptersCheckRouteBeforeTCP443(t *testing.T) {
	input := productionTunDiagnosticInput()
	installDiagnosticResolver(t)
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"198.51.100.10": "eth0"})

	report := runProductionAdapter(t, input, "tcp-443")
	probe := assertProbeClassification(t, report, "tcp-443", tundiag.ClassRouteFailure)
	assertPrivateRouteEvidence(t, probe.Evidence)
	if probe.Evidence.Endpoint != "[endpoint]" {
		t.Fatalf("expected private TCP endpoint evidence, got %#v", probe.Evidence)
	}
}

func TestProductionAdaptersDetectIPv6UplinkLeak(t *testing.T) {
	input := productionTunDiagnosticInput()
	installDiagnosticResolver(t)
	installProductionTunDiagnosticCommandRunner(t, map[string]string{"2001:db8::20": "eth0"})

	report := runProductionAdapter(t, input, "ipv6")
	probe := assertProbeClassification(t, report, "ipv6", tundiag.ClassIPv6Leak)
	assertPrivateRouteEvidence(t, probe.Evidence)
	if probe.Evidence.IPv6 == nil || probe.Evidence.IPv6.DefaultInterface != "[interface]" {
		t.Fatalf("expected private IPv6 evidence, got %#v", probe.Evidence)
	}
}

func assertPrivateRouteEvidence(t *testing.T, evidence tundiag.Evidence) {
	t.Helper()
	if evidence.Route == nil || evidence.Route.Interface != "[interface]" || evidence.Route.Table != "[route-table]" {
		t.Fatalf("expected private structured route evidence, got %#v", evidence)
	}
	if len(evidence.Commands) == 0 || evidence.Commands[0].Command != "[command omitted by diagnostic privacy policy]" || evidence.Commands[0].ExitCode != 0 {
		t.Fatalf("expected private command evidence with preserved exit code, got %#v", evidence.Commands)
	}
}

func runProductionAdapter(t *testing.T, input tunDiagnosticInput, target string) tundiag.Report {
	t.Helper()
	real := buildTunDiagnosticAdapters(input)
	adapters := passingTunDiagnosticAdapters()
	switch target {
	case "session":
		adapters.Session = real.Session
	case "dns-state":
		adapters.DNSState = real.DNSState
	case "dns-system-resolution":
		adapters.SystemResolver = real.SystemResolver
	case "tcp-443":
		adapters.TCP443 = real.TCP443
	case "ipv6":
		adapters.IPv6 = real.IPv6
	default:
		t.Fatalf("unsupported production adapter %q", target)
	}
	return tundiag.Runner{}.Run(context.Background(), tunDiagnosticBase(input), tundiag.StandardProbes(adapters))
}

func passingTunDiagnosticAdapters() tundiag.ProbeAdapters {
	pass := func(context.Context) tundiag.ProbeResult { return tundiag.ProbeResult{Status: tundiag.ProbePass} }
	return tundiag.ProbeAdapters{
		Session: pass, ServerBypass: pass, IPv4Route: pass, DNSState: pass,
		DNSUDP: pass, DNSTCP: pass, SystemResolver: pass, NXDomainIntegrity: pass,
		TCP443: pass, TLS: pass, HTTPSCloudflare: pass, HTTPSGoogle: pass,
		DoHCloudflare: pass, DoHGoogle: pass, IPv6: pass,
		PMTUCloudflare: pass, PMTUHetzner: pass,
	}
}

func productionTunDiagnosticInput() tunDiagnosticInput {
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
	}
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
			result.stdout = fmt.Sprintf("%s dev %s table 51820\n", target, iface)
			return result, nil
		}
		return tunDiagnosticCommandResult{command: command, exitCode: 1, stderr: "unexpected command"}, errors.New("unexpected command: " + command)
	}
	t.Cleanup(func() { tunDiagnosticCommandRunner = original })
}

func installDiagnosticResolver(t *testing.T) {
	t.Helper()
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go serveDiagnosticDNS(server)
			return client, nil
		},
	}
	t.Cleanup(func() { net.DefaultResolver = original })
}

func serveDiagnosticDNS(conn net.Conn) {
	defer conn.Close()
	buffer := make([]byte, 4096)
	count, err := conn.Read(buffer)
	if err != nil {
		return
	}
	query := buffer[:count]
	framed := len(query) > 2 && int(binary.BigEndian.Uint16(query[:2])) == len(query)-2
	if framed {
		query = query[2:]
	}
	response, ok := diagnosticDNSResponse(query)
	if !ok {
		return
	}
	if framed {
		frame := make([]byte, 2+len(response))
		binary.BigEndian.PutUint16(frame[:2], uint16(len(response)))
		copy(frame[2:], response)
		response = frame
	}
	_, _ = conn.Write(response)
}

func diagnosticDNSResponse(query []byte) ([]byte, bool) {
	if len(query) < 17 {
		return nil, false
	}
	questionEnd := 12
	for questionEnd < len(query) && query[questionEnd] != 0 {
		questionEnd += int(query[questionEnd]) + 1
	}
	questionEnd += 5
	if questionEnd > len(query) {
		return nil, false
	}
	recordType := binary.BigEndian.Uint16(query[questionEnd-4 : questionEnd-2])
	header := make([]byte, 12)
	copy(header[:2], query[:2])
	binary.BigEndian.PutUint16(header[2:4], 0x8180)
	binary.BigEndian.PutUint16(header[4:6], 1)
	binary.BigEndian.PutUint16(header[6:8], 1)
	response := append(header, query[12:questionEnd]...)
	response = append(response, 0xc0, 0x0c)
	answer := make([]byte, 10)
	binary.BigEndian.PutUint16(answer[0:2], recordType)
	binary.BigEndian.PutUint16(answer[2:4], 1)
	binary.BigEndian.PutUint32(answer[4:8], 60)
	if recordType == 28 {
		binary.BigEndian.PutUint16(answer[8:10], 16)
		response = append(response, answer...)
		response = append(response, net.ParseIP("2001:db8::20").To16()...)
		return response, true
	}
	binary.BigEndian.PutUint16(answer[8:10], 4)
	response = append(response, answer...)
	response = append(response, 198, 51, 100, 10)
	return response, true
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
