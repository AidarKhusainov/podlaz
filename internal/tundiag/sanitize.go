package tundiag

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/render"
)

const (
	maxDiagnosticText = 4096
	maxEvidenceItems  = 32
)

const (
	privacyProfileName     = "[profile]"
	privacyTransactionID   = "[transaction]"
	privacyInterfaceName   = "[interface]"
	privacyNetworkName     = "[network]"
	privacyAddress         = "[address]"
	privacyEndpoint        = "[endpoint]"
	privacyDomain          = "[domain]"
	privacyTarget          = "[target]"
	privacyRouteTable      = "[route-table]"
	privacyCertificateName = "[certificate-name]"
	privacyDiagnosticText  = "[detail omitted by diagnostic privacy policy]"
	privacyCommand         = "[command omitted by diagnostic privacy policy]"
	privacyCommandOutput   = "[output omitted by diagnostic privacy policy]"
	privacyRule            = "[rule omitted by diagnostic privacy policy]"
	privacyNote            = "[note omitted by diagnostic privacy policy]"
)

// SanitizeReport is the public diagnostics privacy boundary used before
// persistence and before both human and JSON rendering. It deliberately keeps
// classifications, statuses, durations, MTUs, response codes, and other safe
// structural evidence while replacing user-, profile-, host-, and
// network-specific identifiers with typed placeholders. Arbitrary command,
// error, route/rule, and note payloads are not published because their content
// cannot be proven identifier-free.
func SanitizeReport(report Report) Report {
	report.FailurePhase = sanitize(report.FailurePhase)
	report.RollbackStatus = sanitize(report.RollbackStatus)
	report.Session.State = sanitize(report.Session.State)
	report.Session.Mode = sanitize(report.Session.Mode)
	report.Session.ProfileName = privacyValue(report.Session.ProfileName, privacyProfileName)
	report.Session.TransactionID = privacyValue(report.Session.TransactionID, privacyTransactionID)
	report.Session.Interface = privacyManagedInterface(report.Session.Interface)
	report.Session.MetadataSource = privacyValue(report.Session.MetadataSource, privacyDiagnosticText)

	report.Network.PhysicalInterface = privacyValue(report.Network.PhysicalInterface, privacyInterfaceName)
	report.Network.SSID = privacyValue(report.Network.SSID, privacyNetworkName)
	report.Network.Gateway = privacyValue(report.Network.Gateway, privacyAddress)
	report.Network.LocalAddresses = privacySlice(report.Network.LocalAddresses, privacyAddress)
	report.Network.TunInterface = privacyManagedInterface(report.Network.TunInterface)
	report.Network.DNSServers = privacySlice(report.Network.DNSServers, privacyAddress)
	report.Network.IPv4Status = sanitize(report.Network.IPv4Status)
	report.Network.IPv6Status = sanitize(report.Network.IPv6Status)
	report.Network.ServerEndpoint = privacyValue(report.Network.ServerEndpoint, privacyEndpoint)
	report.Network.ServerHostname = privacyValue(report.Network.ServerHostname, privacyDomain)
	report.Network.ServerName = privacyValue(report.Network.ServerName, privacyDomain)
	report.Network.ServerAddresses = privacySlice(report.Network.ServerAddresses, privacyAddress)
	report.Network.DoHProviders = privacySlice(report.Network.DoHProviders, privacyEndpoint)
	report.Network.NftablesStatus = privacyValue(report.Network.NftablesStatus, privacyDiagnosticText)
	report.Warnings = privacyRequiredSlice(report.Warnings, privacyDiagnosticText)
	report.Errors = privacyRequiredSlice(report.Errors, privacyDiagnosticText)
	report.ReportPath = sanitize(report.ReportPath)
	if report.Probes == nil {
		report.Probes = []ProbeResult{}
	}
	for i := range report.Probes {
		report.Probes[i] = SanitizeProbeResult(report.Probes[i])
	}
	return report
}

func SanitizeProbeResult(result ProbeResult) ProbeResult {
	result.ID = sanitize(result.ID)
	result.Target = privacyValue(result.Target, privacyTarget)
	result.FailurePhase = FailurePhase(sanitize(string(result.FailurePhase)))
	result.Error = privacyValue(result.Error, privacyDiagnosticText)
	result.DependencyReason = privacyValue(result.DependencyReason, privacyDiagnosticText)
	result.Evidence.Endpoint = privacyValue(result.Evidence.Endpoint, privacyEndpoint)
	result.Evidence.ResolvedAddresses = privacySlice(result.Evidence.ResolvedAddresses, privacyAddress)
	result.Evidence.PolicyRules = privacySlice(result.Evidence.PolicyRules, privacyRule)
	result.Evidence.NftablesRules = privacySlice(result.Evidence.NftablesRules, privacyRule)
	result.Evidence.Notes = privacySlice(result.Evidence.Notes, privacyNote)
	if result.Evidence.Route != nil {
		route := *result.Evidence.Route
		route.Destination = privacyValue(route.Destination, privacyAddress)
		route.Interface = privacyValue(route.Interface, privacyInterfaceName)
		route.Gateway = privacyValue(route.Gateway, privacyAddress)
		route.Table = privacyValue(route.Table, privacyRouteTable)
		route.Rule = privacyValue(route.Rule, privacyRule)
		route.Raw = privacyValue(route.Raw, privacyDiagnosticText)
		result.Evidence.Route = &route
	}
	if result.Evidence.DNS != nil {
		dns := *result.Evidence.DNS
		dns.Server = privacyValue(dns.Server, privacyAddress)
		dns.Name = privacyValue(dns.Name, privacyDomain)
		dns.Addresses = privacySlice(dns.Addresses, privacyAddress)
		result.Evidence.DNS = &dns
	}
	if result.Evidence.TLS != nil {
		tls := *result.Evidence.TLS
		tls.Version = sanitize(tls.Version)
		tls.Cipher = sanitize(tls.Cipher)
		tls.PeerSubject = privacyValue(tls.PeerSubject, privacyCertificateName)
		tls.PeerIssuer = privacyValue(tls.PeerIssuer, privacyCertificateName)
		result.Evidence.TLS = &tls
	}
	if result.Evidence.HTTP != nil {
		http := *result.Evidence.HTTP
		http.Location = privacyValue(http.Location, privacyEndpoint)
		http.FailurePhase = sanitize(http.FailurePhase)
		result.Evidence.HTTP = &http
	}
	if result.Evidence.IPv6 != nil {
		ipv6 := *result.Evidence.IPv6
		ipv6.State = sanitize(ipv6.State)
		ipv6.UplinkAddresses = privacySlice(ipv6.UplinkAddresses, privacyAddress)
		ipv6.TunAddresses = privacySlice(ipv6.TunAddresses, privacyAddress)
		ipv6.DefaultInterface = privacyValue(ipv6.DefaultInterface, privacyInterfaceName)
		ipv6.RouteTable = privacyValue(ipv6.RouteTable, privacyRouteTable)
		result.Evidence.IPv6 = &ipv6
	}
	if len(result.Evidence.Commands) > maxEvidenceItems {
		result.Evidence.Commands = append([]CommandEvidence(nil), result.Evidence.Commands[:maxEvidenceItems]...)
	}
	for i := range result.Evidence.Commands {
		result.Evidence.Commands[i].Command = privacyValue(result.Evidence.Commands[i].Command, privacyCommand)
		result.Evidence.Commands[i].Stdout = privacyValue(result.Evidence.Commands[i].Stdout, privacyCommandOutput)
		result.Evidence.Commands[i].Stderr = privacyValue(result.Evidence.Commands[i].Stderr, privacyCommandOutput)
	}
	return result
}

func sanitize(value string) string {
	return limitText(render.Redact(value), maxDiagnosticText)
}

func privacyManagedInterface(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if value == "podlaz0" {
		return value
	}
	return privacyInterfaceName
}

func privacyValue(value, placeholder string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return placeholder
}

func privacyRequiredSlice(values []string, placeholder string) []string {
	values = privacySlice(values, placeholder)
	if values == nil {
		return []string{}
	}
	return values
}

func privacySlice(values []string, placeholder string) []string {
	if len(values) == 0 {
		return nil
	}
	if len(values) > maxEvidenceItems {
		values = values[:maxEvidenceItems]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, placeholder)
		}
	}
	return out
}

func limitText(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= len("…") {
		return value[:limit]
	}
	return value[:limit-len("…")] + "…"
}
