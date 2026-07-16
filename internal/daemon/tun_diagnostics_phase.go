package daemon

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/AidarKhusainov/podlaz/internal/network/planner"
	netsnapshot "github.com/AidarKhusainov/podlaz/internal/network/snapshot"
	"github.com/AidarKhusainov/podlaz/internal/tundiag"
)

func buildPhaseAwareTunDiagnosticAdapters(input tunDiagnosticInput) tundiag.ProbeAdapters {
	adapters := buildHardenedTunDiagnosticAdapters(input)
	client := tundiag.NetworkClient{}
	adapters.TCP443 = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunTCPRouteWithPhase(ctx, client, input.plan, "www.cloudflare.com", 443)
	}
	adapters.TLS = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunTLSWithPhase(ctx, client, "www.cloudflare.com", 443)
	}
	adapters.HTTPSCloudflare = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSWithPhase(ctx, client, "https-cloudflare-small", tundiag.ClassHTTPSFailure)
	}
	adapters.HTTPSGoogle = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSWithPhase(ctx, client, "https-google-small", tundiag.ClassHTTPSFailure)
	}
	adapters.PMTUCloudflare = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSWithPhase(ctx, client, "pmtu-cloudflare-16k", tundiag.ClassHTTPSFailure)
	}
	adapters.PMTUHetzner = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunHTTPSWithPhase(ctx, client, "pmtu-hetzner-16k", tundiag.ClassHTTPSFailure)
	}
	adapters.DoHCloudflare = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunDoHWithPhase(ctx, "doh-cloudflare")
	}
	adapters.DoHGoogle = func(ctx context.Context) tundiag.ProbeResult {
		return probeTunDoHWithPhase(ctx, "doh-google")
	}
	return adapters
}

func probeTunTCPRouteWithPhase(ctx context.Context, client tundiag.NetworkClient, plan planner.TunPlan, host string, port uint16) tundiag.ProbeResult {
	addresses, err := client.Resolve(ctx, host)
	if err != nil {
		return tundiag.ProbeResult{
			Status:         tundiag.ProbeFail,
			Classification: tundiag.ClassDNSResolutionFailure,
			FailurePhase:   tundiag.FailurePhaseDNSResolution,
			Error:          err.Error(),
		}
	}
	address := preferredAddress(addresses, false)
	if address == "" {
		return tundiag.ProbeResult{
			Status:         tundiag.ProbeFail,
			Classification: tundiag.ClassDNSResolutionFailure,
			FailurePhase:   tundiag.FailurePhaseDNSResolution,
			Error:          "resolver returned no usable IPv4 address",
			Evidence:       tundiag.Evidence{ResolvedAddresses: addresses},
		}
	}
	route, command, err := lookupTunRouteForAddress(ctx, address)
	result := tundiag.ProbeResult{
		Evidence: tundiag.Evidence{
			Endpoint:          net.JoinHostPort(address, strconv.Itoa(int(port))),
			ResolvedAddresses: addresses,
			Route:             &route,
			Commands:          []tundiag.CommandEvidence{command},
		},
	}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.FailurePhase = tundiag.FailurePhaseRouteLookup
		result.Error = "lookup route to TCP target: " + err.Error()
		return result
	}
	expectedInterface := emptyAs(plan.TunDevice.Name, netsnapshot.DefaultTunName)
	if route.Interface != expectedInterface {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassRouteFailure
		result.FailurePhase = tundiag.FailurePhaseRouteLookup
		result.Error = fmt.Sprintf("TCP target %s routes through %s; expected %s", address, route.Interface, expectedInterface)
		return result
	}
	duration, err := client.TCP(ctx, address, port)
	result.DurationMS = duration.Milliseconds()
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassTCP443Failure
		result.FailurePhase = tundiag.FailurePhaseTCPConnect
		result.Error = err.Error()
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunTLSWithPhase(ctx context.Context, client tundiag.NetworkClient, host string, port uint16) tundiag.ProbeResult {
	evidence, phase, err := client.TLSWithFailurePhase(ctx, host, port)
	result := tundiag.ProbeResult{
		FailurePhase: phase,
		Evidence:     tundiag.Evidence{Endpoint: net.JoinHostPort(host, strconv.Itoa(int(port))), TLS: &evidence},
	}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassTLSFailure
		result.Error = err.Error()
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunHTTPSWithPhase(ctx context.Context, client tundiag.NetworkClient, targetID string, classification tundiag.Classification) tundiag.ProbeResult {
	target, ok := tundiag.FindTarget(targetID)
	if !ok {
		return diagnosticFailure(tundiag.ClassInternalDiagnosticError, "missing endpoint catalog target "+targetID)
	}
	evidence, phase, err := client.HTTPSWithFailurePhase(ctx, target)
	result := tundiag.ProbeResult{
		FailurePhase: phase,
		Evidence:     tundiag.Evidence{Endpoint: target.URL, HTTP: &evidence},
	}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = classification
		result.Error = err.Error()
		return result
	}
	if target.Kind == tundiag.TargetPMTU && evidence.BytesRead < target.MaxResponseBytes {
		evidence.FailurePhase = "short_body"
		result.Evidence.HTTP = &evidence
		result.Status = tundiag.ProbeFail
		result.Classification = classification
		result.FailurePhase = tundiag.FailurePhaseHTTPBody
		result.Error = fmt.Sprintf("bounded transfer returned %d of %d requested bytes", evidence.BytesRead, target.MaxResponseBytes)
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}

func probeTunDoHWithPhase(ctx context.Context, targetID string) tundiag.ProbeResult {
	target, ok := tundiag.FindTarget(targetID)
	if !ok {
		return diagnosticFailure(tundiag.ClassInternalDiagnosticError, "missing endpoint catalog target "+targetID)
	}
	client := tundiag.NetworkClient{DialContext: tunDiagnosticBootstrapDialer(target)}
	dnsEvidence, httpEvidence, phase, err := client.DoHWithFailurePhase(ctx, target, "example.com", tundiag.DNSRecordTypeA)
	result := tundiag.ProbeResult{
		FailurePhase: phase,
		Evidence: tundiag.Evidence{
			Endpoint: target.URL,
			DNS:      &dnsEvidence,
			HTTP:     &httpEvidence,
		},
	}
	if err != nil {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassDoHFailure
		result.Error = err.Error()
		return result
	}
	if dnsEvidence.ResponseCode != tundiag.DNSRCodeSuccess || len(dnsEvidence.Addresses) == 0 {
		result.Status = tundiag.ProbeFail
		result.Classification = tundiag.ClassDoHFailure
		result.FailurePhase = tundiag.FailurePhaseHTTPResponse
		result.Error = fmt.Sprintf("DoH response code=%d addresses=%d", dnsEvidence.ResponseCode, len(dnsEvidence.Addresses))
		return result
	}
	result.Status = tundiag.ProbePass
	return result
}
