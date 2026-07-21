package tundiag

import (
	"strings"

	"github.com/AidarKhusainov/podlaz/internal/render"
)

const (
	maxDiagnosticText = 4096
	maxEvidenceItems  = 32
)

func SanitizeReport(report Report) Report {
	report.FailurePhase = sanitize(report.FailurePhase)
	report.RollbackStatus = sanitize(report.RollbackStatus)
	report.Session.State = sanitize(report.Session.State)
	report.Session.Mode = sanitize(report.Session.Mode)
	report.Session.ProfileName = sanitize(report.Session.ProfileName)
	report.Session.TransactionID = sanitize(report.Session.TransactionID)
	report.Session.Interface = sanitize(report.Session.Interface)
	report.Session.MetadataSource = sanitize(report.Session.MetadataSource)

	report.Network.PhysicalInterface = sanitize(report.Network.PhysicalInterface)
	report.Network.SSID = sanitize(report.Network.SSID)
	report.Network.Gateway = sanitize(report.Network.Gateway)
	report.Network.LocalAddresses = sanitizeSlice(report.Network.LocalAddresses)
	report.Network.TunInterface = sanitize(report.Network.TunInterface)
	report.Network.DNSServers = sanitizeSlice(report.Network.DNSServers)
	report.Network.IPv4Status = sanitize(report.Network.IPv4Status)
	report.Network.IPv6Status = sanitize(report.Network.IPv6Status)
	report.Network.ServerEndpoint = sanitize(report.Network.ServerEndpoint)
	report.Network.ServerHostname = sanitize(report.Network.ServerHostname)
	report.Network.ServerName = sanitize(report.Network.ServerName)
	report.Network.ServerAddresses = sanitizeSlice(report.Network.ServerAddresses)
	report.Network.DoHProviders = sanitizeSlice(report.Network.DoHProviders)
	report.Network.NftablesStatus = sanitize(report.Network.NftablesStatus)
	report.Warnings = sanitizeRequiredSlice(report.Warnings)
	report.Errors = sanitizeRequiredSlice(report.Errors)
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
	result.Target = sanitize(result.Target)
	result.FailurePhase = FailurePhase(sanitize(string(result.FailurePhase)))
	result.Error = sanitize(result.Error)
	result.DependencyReason = sanitize(result.DependencyReason)
	result.Evidence.Endpoint = sanitize(result.Evidence.Endpoint)
	result.Evidence.ResolvedAddresses = sanitizeSlice(result.Evidence.ResolvedAddresses)
	result.Evidence.PolicyRules = sanitizeSlice(result.Evidence.PolicyRules)
	result.Evidence.NftablesRules = sanitizeSlice(result.Evidence.NftablesRules)
	result.Evidence.Notes = sanitizeSlice(result.Evidence.Notes)
	if result.Evidence.Route != nil {
		route := *result.Evidence.Route
		route.Destination = sanitize(route.Destination)
		route.Interface = sanitize(route.Interface)
		route.Gateway = sanitize(route.Gateway)
		route.Table = sanitize(route.Table)
		route.Rule = sanitize(route.Rule)
		route.Raw = sanitize(route.Raw)
		result.Evidence.Route = &route
	}
	if result.Evidence.DNS != nil {
		dns := *result.Evidence.DNS
		dns.Server = sanitize(dns.Server)
		dns.Name = sanitize(dns.Name)
		dns.Addresses = sanitizeSlice(dns.Addresses)
		result.Evidence.DNS = &dns
	}
	if result.Evidence.TLS != nil {
		tls := *result.Evidence.TLS
		tls.Version = sanitize(tls.Version)
		tls.Cipher = sanitize(tls.Cipher)
		tls.PeerSubject = sanitize(tls.PeerSubject)
		tls.PeerIssuer = sanitize(tls.PeerIssuer)
		result.Evidence.TLS = &tls
	}
	if result.Evidence.HTTP != nil {
		http := *result.Evidence.HTTP
		http.Location = sanitize(http.Location)
		result.Evidence.HTTP = &http
	}
	if result.Evidence.IPv6 != nil {
		ipv6 := *result.Evidence.IPv6
		ipv6.State = sanitize(ipv6.State)
		ipv6.UplinkAddresses = sanitizeSlice(ipv6.UplinkAddresses)
		ipv6.TunAddresses = sanitizeSlice(ipv6.TunAddresses)
		ipv6.DefaultInterface = sanitize(ipv6.DefaultInterface)
		ipv6.RouteTable = sanitize(ipv6.RouteTable)
		result.Evidence.IPv6 = &ipv6
	}
	if len(result.Evidence.Commands) > maxEvidenceItems {
		result.Evidence.Commands = append([]CommandEvidence(nil), result.Evidence.Commands[:maxEvidenceItems]...)
	}
	for i := range result.Evidence.Commands {
		result.Evidence.Commands[i].Command = sanitize(result.Evidence.Commands[i].Command)
		result.Evidence.Commands[i].Stdout = sanitize(result.Evidence.Commands[i].Stdout)
		result.Evidence.Commands[i].Stderr = sanitize(result.Evidence.Commands[i].Stderr)
	}
	return result
}

func sanitize(value string) string {
	return limitText(render.Redact(value), maxDiagnosticText)
}

func sanitizeRequiredSlice(values []string) []string {
	values = sanitizeSlice(values)
	if values == nil {
		return []string{}
	}
	return values
}

func sanitizeSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	if len(values) > maxEvidenceItems {
		values = values[:maxEvidenceItems]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = sanitize(value)
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
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
